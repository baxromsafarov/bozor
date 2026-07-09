// Package payme — адаптер провайдера Payme: выставление счёта (checkout-URL) и
// обработчик merchant JSON-RPC колбэков (CheckPerformTransaction, CreateTransaction,
// PerformTransaction, CancelTransaction, CheckTransaction). Колбэки идемпотентны:
// повторный PerformTransaction по той же транзакции не зачисляет средства дважды.
package payme

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

// tiyinPerUZS — Payme оперирует суммами в тийинах (1 UZS = 100 тийин).
const tiyinPerUZS = 100

const checkoutBase = "https://checkout.paycom.uz/"

// Состояния транзакции Payme.
const (
	stateCreated   = 1
	statePerformed = 2
	stateCanceled  = -1
)

// Коды ошибок JSON-RPC Payme.
const (
	errAuth          = -32504
	errParse         = -32700
	errMethod        = -32601
	errAmount        = -31001
	errTxnNotFound   = -31003
	errCannotPerform = -31008
	errAccount       = -31050
)

// PaymentOps — операции над платежами, нужные обработчику Payme.
type PaymentOps interface {
	ByID(ctx context.Context, id string) (domain.Payment, bool, error)
	ByExternal(ctx context.Context, externalID string) (domain.Payment, bool, error)
	Bind(ctx context.Context, id, externalID string) error
	Confirm(ctx context.Context, id, externalID string) (domain.Payment, bool, error)
	Cancel(ctx context.Context, id string) (domain.Payment, bool, error)
}

// Provider реализует provider.Provider (внешняя часть) и обслуживает колбэки.
type Provider struct {
	merchantID string
	key        string
	ops        PaymentOps
	log        *slog.Logger
}

// New создаёт адаптер Payme.
func New(merchantID, key string, ops PaymentOps, log *slog.Logger) *Provider {
	return &Provider{merchantID: merchantID, key: key, ops: ops, log: log}
}

// Name возвращает код провайдера.
func (p *Provider) Name() string { return domain.ProviderPayme }

// CreateInvoice формирует ссылку оплаты Payme: base64 параметров
// m=<merchant>;ac.payment_id=<id>;a=<сумма в тийинах>.
func (p *Provider) CreateInvoice(_ context.Context, inv provider.Invoice) (provider.InvoiceResult, error) {
	params := "m=" + p.merchantID + ";ac.payment_id=" + inv.PaymentID + ";a=" + strconv.FormatInt(inv.AmountUZS*tiyinPerUZS, 10)
	return provider.InvoiceResult{
		CheckoutURL: checkoutBase + base64.StdEncoding.EncodeToString([]byte(params)),
	}, nil
}

// rpcRequest — конверт JSON-RPC запроса Payme.
type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params rpcParams       `json:"params"`
}

type rpcParams struct {
	ID      string          `json:"id"`      // id транзакции Payme
	Time    int64           `json:"time"`    // время (мс)
	Amount  int64           `json:"amount"`  // сумма в тийинах
	Account map[string]any  `json:"account"` // {payment_id: ...}
	Reason  json.RawMessage `json:"reason"`
}

// ServeHTTP обрабатывает merchant JSON-RPC колбэк Payme (Basic-auth Paycom:key).
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.authorized(r) {
		writeError(w, nil, errAuth, "Недостаточно привилегий", "Ruxsat yetarli emas")
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, nil, errParse, "Ошибка разбора", "Tahlil xatosi")
		return
	}

	switch req.Method {
	case "CheckPerformTransaction":
		p.checkPerform(r.Context(), w, req)
	case "CreateTransaction":
		p.createTransaction(r.Context(), w, req)
	case "PerformTransaction":
		p.performTransaction(r.Context(), w, req)
	case "CancelTransaction":
		p.cancelTransaction(r.Context(), w, req)
	case "CheckTransaction":
		p.checkTransaction(r.Context(), w, req)
	default:
		writeError(w, req.ID, errMethod, "Метод не найден", "Metod topilmadi")
	}
}

func (p *Provider) authorized(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return user == "Paycom" && subtle.ConstantTimeCompare([]byte(pass), []byte(p.key)) == 1
}

// paymentID извлекает наш id платежа из account.payment_id.
func paymentID(params rpcParams) string {
	if v, ok := params.Account["payment_id"].(string); ok {
		return v
	}
	return ""
}

func (p *Provider) checkPerform(ctx context.Context, w http.ResponseWriter, req rpcRequest) {
	pay, found, err := p.ops.ByID(ctx, paymentID(req.Params))
	if err != nil {
		p.internal(w, req.ID, err)
		return
	}
	if !found || pay.Purpose != domain.PurposeTopup {
		writeError(w, req.ID, errAccount, "Заказ не найден", "Buyurtma topilmadi")
		return
	}
	if req.Params.Amount != pay.AmountUZS*tiyinPerUZS {
		writeError(w, req.ID, errAmount, "Неверная сумма", "Noto'g'ri summa")
		return
	}
	if pay.Status != domain.PaymentPending {
		writeError(w, req.ID, errCannotPerform, "Операция невозможна", "Amal imkonsiz")
		return
	}
	writeResult(w, req.ID, map[string]any{"allow": true})
}

func (p *Provider) createTransaction(ctx context.Context, w http.ResponseWriter, req rpcRequest) {
	pay, found, err := p.ops.ByID(ctx, paymentID(req.Params))
	if err != nil {
		p.internal(w, req.ID, err)
		return
	}
	if !found {
		writeError(w, req.ID, errAccount, "Заказ не найден", "Buyurtma topilmadi")
		return
	}
	if req.Params.Amount != pay.AmountUZS*tiyinPerUZS {
		writeError(w, req.ID, errAmount, "Неверная сумма", "Noto'g'ri summa")
		return
	}
	// Идемпотентность: транзакция уже привязана → возвращаем тот же результат.
	if pay.ExternalID != nil && *pay.ExternalID != req.Params.ID {
		writeError(w, req.ID, errCannotPerform, "Заказ занят другой транзакцией", "Buyurtma band")
		return
	}
	if pay.Status != domain.PaymentPending {
		writeError(w, req.ID, errCannotPerform, "Операция невозможна", "Amal imkonsiz")
		return
	}
	if err := p.ops.Bind(ctx, pay.ID, req.Params.ID); err != nil {
		p.internal(w, req.ID, err)
		return
	}
	writeResult(w, req.ID, map[string]any{
		"create_time": pay.CreatedAt.UnixMilli(),
		"transaction": pay.ID,
		"state":       stateCreated,
	})
}

func (p *Provider) performTransaction(ctx context.Context, w http.ResponseWriter, req rpcRequest) {
	pay, found, err := p.ops.ByExternal(ctx, req.Params.ID)
	if err != nil {
		p.internal(w, req.ID, err)
		return
	}
	if !found {
		writeError(w, req.ID, errTxnNotFound, "Транзакция не найдена", "Tranzaksiya topilmadi")
		return
	}
	confirmed, _, err := p.ops.Confirm(ctx, pay.ID, req.Params.ID)
	if err != nil {
		p.internal(w, req.ID, err)
		return
	}
	writeResult(w, req.ID, map[string]any{
		"perform_time": confirmed.UpdatedAt.UnixMilli(),
		"transaction":  confirmed.ID,
		"state":        statePerformed,
	})
}

func (p *Provider) cancelTransaction(ctx context.Context, w http.ResponseWriter, req rpcRequest) {
	pay, found, err := p.ops.ByExternal(ctx, req.Params.ID)
	if err != nil {
		p.internal(w, req.ID, err)
		return
	}
	if !found {
		writeError(w, req.ID, errTxnNotFound, "Транзакция не найдена", "Tranzaksiya topilmadi")
		return
	}
	canceled, _, err := p.ops.Cancel(ctx, pay.ID)
	if err != nil {
		p.internal(w, req.ID, err)
		return
	}
	writeResult(w, req.ID, map[string]any{
		"cancel_time": canceled.UpdatedAt.UnixMilli(),
		"transaction": canceled.ID,
		"state":       stateCanceled,
	})
}

func (p *Provider) checkTransaction(ctx context.Context, w http.ResponseWriter, req rpcRequest) {
	pay, found, err := p.ops.ByExternal(ctx, req.Params.ID)
	if err != nil {
		p.internal(w, req.ID, err)
		return
	}
	if !found {
		writeError(w, req.ID, errTxnNotFound, "Транзакция не найдена", "Tranzaksiya topilmadi")
		return
	}
	var performTime, cancelTime int64
	state := stateCreated
	switch pay.Status {
	case domain.PaymentSucceeded:
		state, performTime = statePerformed, pay.UpdatedAt.UnixMilli()
	case domain.PaymentFailed, domain.PaymentCanceled:
		state, cancelTime = stateCanceled, pay.UpdatedAt.UnixMilli()
	}
	writeResult(w, req.ID, map[string]any{
		"create_time":  pay.CreatedAt.UnixMilli(),
		"perform_time": performTime,
		"cancel_time":  cancelTime,
		"transaction":  pay.ID,
		"state":        state,
		"reason":       nil,
	})
}

func (p *Provider) internal(w http.ResponseWriter, id json.RawMessage, err error) {
	p.log.Error("payme колбэк", slog.String("error", err.Error()))
	writeError(w, id, errCannotPerform, "Внутренняя ошибка", "Ichki xato")
}

// writeResult/writeError пишут JSON-RPC ответ Payme (HTTP 200 всегда).
func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeRPC(w, map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": result})
}

func writeError(w http.ResponseWriter, id json.RawMessage, code int, ru, uz string) {
	writeRPC(w, map[string]any{"jsonrpc": "2.0", "id": rawID(id), "error": map[string]any{
		"code": code, "message": map[string]string{"ru": ru, "uz": uz, "en": ru},
	}})
}

func writeRPC(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

// rawID возвращает id запроса как есть (null, если отсутствует).
func rawID(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	return id
}
