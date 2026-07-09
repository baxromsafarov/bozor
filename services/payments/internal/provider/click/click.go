// Package click — адаптер провайдера Click: выставление счёта (checkout-URL) и
// двухфазный колбэк Prepare/Complete с проверкой подписи MD5. Колбэки идемпотентны:
// повторный Complete по той же транзакции не зачисляет средства дважды.
package click

import (
	"context"
	"crypto/md5" //nolint:gosec // алгоритм подписи задан протоколом Click
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

const checkoutBase = "https://my.click.uz/services/pay"

// Действия колбэка Click.
const (
	actionPrepare  = "0"
	actionComplete = "1"
)

// Коды ошибок Click.
const (
	errOK        = 0
	errSign      = -1
	errAmount    = -2
	errAlready   = -4
	errNoOrder   = -5
	errNoTxn     = -6
	errCancelled = -9
)

// PaymentOps — операции над платежами, нужные обработчику Click.
type PaymentOps interface {
	ByID(ctx context.Context, id string) (domain.Payment, bool, error)
	Bind(ctx context.Context, id, externalID string) error
	Confirm(ctx context.Context, id, externalID string) (domain.Payment, bool, error)
}

// Provider реализует provider.Provider и обслуживает колбэки Click.
type Provider struct {
	serviceID  string
	merchantID string
	secretKey  string
	ops        PaymentOps
	log        *slog.Logger
}

// New создаёт адаптер Click.
func New(serviceID, merchantID, secretKey string, ops PaymentOps, log *slog.Logger) *Provider {
	return &Provider{serviceID: serviceID, merchantID: merchantID, secretKey: secretKey, ops: ops, log: log}
}

// Name возвращает код провайдера.
func (p *Provider) Name() string { return domain.ProviderClick }

// CreateInvoice формирует ссылку оплаты Click (service_id/merchant_id/amount +
// transaction_param = наш id платежа).
func (p *Provider) CreateInvoice(_ context.Context, inv provider.Invoice) (provider.InvoiceResult, error) {
	q := url.Values{}
	q.Set("service_id", p.serviceID)
	q.Set("merchant_id", p.merchantID)
	q.Set("amount", strconv.FormatInt(inv.AmountUZS, 10))
	q.Set("transaction_param", inv.PaymentID)
	return provider.InvoiceResult{CheckoutURL: checkoutBase + "?" + q.Encode()}, nil
}

type response struct {
	ClickTransID      string `json:"click_trans_id"`
	MerchantTransID   string `json:"merchant_trans_id"`
	MerchantPrepareID string `json:"merchant_prepare_id,omitempty"`
	MerchantConfirmID string `json:"merchant_confirm_id,omitempty"`
	Error             int    `json:"error"`
	ErrorNote         string `json:"error_note"`
}

// ServeHTTP обрабатывает колбэк Click (form-encoded): action=0 Prepare, 1 Complete.
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, response{Error: errSign, ErrorNote: "bad request"})
		return
	}
	f := r.PostForm
	action := f.Get("action")

	switch action {
	case actionPrepare:
		p.prepare(r.Context(), w, f)
	case actionComplete:
		p.complete(r.Context(), w, f)
	default:
		writeJSON(w, response{Error: errNoTxn, ErrorNote: "unknown action"})
	}
}

func (p *Provider) prepare(ctx context.Context, w http.ResponseWriter, f url.Values) {
	resp := response{ClickTransID: f.Get("click_trans_id"), MerchantTransID: f.Get("merchant_trans_id")}

	// Подпись: md5(click_trans_id + service_id + secret + merchant_trans_id + amount + action + sign_time).
	want := sign(f.Get("click_trans_id"), p.serviceID, p.secretKey, f.Get("merchant_trans_id"),
		f.Get("amount"), f.Get("action"), f.Get("sign_time"))
	if f.Get("sign_string") != want {
		resp.Error, resp.ErrorNote = errSign, "SIGN CHECK FAILED"
		writeJSON(w, resp)
		return
	}

	pay, found, err := p.ops.ByID(ctx, f.Get("merchant_trans_id"))
	if err != nil {
		p.log.Error("click prepare", slog.String("error", err.Error()))
		resp.Error, resp.ErrorNote = errNoOrder, "internal error"
		writeJSON(w, resp)
		return
	}
	if !found || pay.Purpose != domain.PurposeTopup {
		resp.Error, resp.ErrorNote = errNoOrder, "order not found"
		writeJSON(w, resp)
		return
	}
	if !amountMatches(f.Get("amount"), pay.AmountUZS) {
		resp.Error, resp.ErrorNote = errAmount, "incorrect amount"
		writeJSON(w, resp)
		return
	}
	if pay.Status == domain.PaymentSucceeded {
		resp.Error, resp.ErrorNote = errAlready, "already paid"
		writeJSON(w, resp)
		return
	}
	if pay.Status != domain.PaymentPending {
		resp.Error, resp.ErrorNote = errCancelled, "transaction cancelled"
		writeJSON(w, resp)
		return
	}
	if err := p.ops.Bind(ctx, pay.ID, f.Get("click_trans_id")); err != nil {
		p.log.Error("click bind", slog.String("error", err.Error()))
		resp.Error, resp.ErrorNote = errNoOrder, "internal error"
		writeJSON(w, resp)
		return
	}
	// merchant_prepare_id — наш идентификатор этапа подготовки (Click вернёт его в Complete).
	resp.MerchantPrepareID = f.Get("click_trans_id")
	resp.Error, resp.ErrorNote = errOK, "Success"
	writeJSON(w, resp)
}

func (p *Provider) complete(ctx context.Context, w http.ResponseWriter, f url.Values) {
	resp := response{ClickTransID: f.Get("click_trans_id"), MerchantTransID: f.Get("merchant_trans_id")}

	// Подпись включает merchant_prepare_id.
	want := sign(f.Get("click_trans_id"), p.serviceID, p.secretKey, f.Get("merchant_trans_id"),
		f.Get("merchant_prepare_id"), f.Get("amount"), f.Get("action"), f.Get("sign_time"))
	if f.Get("sign_string") != want {
		resp.Error, resp.ErrorNote = errSign, "SIGN CHECK FAILED"
		writeJSON(w, resp)
		return
	}

	pay, found, err := p.ops.ByID(ctx, f.Get("merchant_trans_id"))
	if err != nil {
		p.log.Error("click complete", slog.String("error", err.Error()))
		resp.Error, resp.ErrorNote = errNoTxn, "internal error"
		writeJSON(w, resp)
		return
	}
	if !found {
		resp.Error, resp.ErrorNote = errNoTxn, "transaction not found"
		writeJSON(w, resp)
		return
	}
	if !amountMatches(f.Get("amount"), pay.AmountUZS) {
		resp.Error, resp.ErrorNote = errAmount, "incorrect amount"
		writeJSON(w, resp)
		return
	}
	confirmed, _, err := p.ops.Confirm(ctx, pay.ID, f.Get("click_trans_id"))
	if err != nil {
		p.log.Error("click confirm", slog.String("error", err.Error()))
		resp.Error, resp.ErrorNote = errNoTxn, "internal error"
		writeJSON(w, resp)
		return
	}
	resp.MerchantConfirmID = confirmed.ID
	resp.Error, resp.ErrorNote = errOK, "Success"
	writeJSON(w, resp)
}

// sign считает MD5 конкатенации частей подписи Click.
func sign(parts ...string) string {
	joined := ""
	for _, s := range parts {
		joined += s
	}
	sum := md5.Sum([]byte(joined)) //nolint:gosec // алгоритм задан Click
	return hex.EncodeToString(sum[:])
}

// amountMatches сверяет сумму Click (сум с копейками) с суммой платежа (UZS).
func amountMatches(raw string, amountUZS int64) bool {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return false
	}
	return math.Abs(v-float64(amountUZS)) < 0.5
}

func writeJSON(w http.ResponseWriter, resp response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
