package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

// maxTopupBody — предел тела запроса пополнения.
const maxTopupBody = 4 << 10

// Payments — use-cases платежей (реализуется app.PaymentService).
type Payments interface {
	InitTopup(ctx context.Context, userID string, amount int64, providerName string) (domain.Payment, provider.InvoiceResult, error)
	GetPayment(ctx context.Context, id string) (domain.Payment, bool, error)
	// ConfirmMock подтверждает mock-платёж (dev-имитация колбэка провайдера).
	ConfirmMock(ctx context.Context, id string) (domain.Payment, bool, error)
}

// PaymentHandler обслуживает инициацию пополнения и dev-колбэк mock-провайдера.
type PaymentHandler struct {
	svc Payments
	log *slog.Logger
}

// NewPaymentHandler создаёт обработчик платежей.
func NewPaymentHandler(svc Payments, log *slog.Logger) *PaymentHandler {
	return &PaymentHandler{svc: svc, log: log}
}

type topupRequest struct {
	AmountUZS int64  `json:"amount_uzs"`
	Provider  string `json:"provider"`
}

type topupResponse struct {
	PaymentID   string            `json:"payment_id"`
	Provider    string            `json:"provider"`
	AmountUZS   int64             `json:"amount_uzs"`
	Currency    string            `json:"currency"`
	Status      string            `json:"status"`
	CheckoutURL string            `json:"checkout_url"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// Topup инициирует пополнение кошелька через провайдера: создаёт платёж (pending)
// и возвращает ссылку оплаты. Средства зачислятся после колбэка провайдера.
func (h *PaymentHandler) Topup(w http.ResponseWriter, r *http.Request) {
	userID := authx.UserID(r.Context())

	var req topupRequest
	if err := httpx.DecodeJSON(w, r, &req, maxTopupBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.Provider == "" {
		req.Provider = domain.ProviderMock
	}

	pay, inv, err := h.svc.InitTopup(r.Context(), userID, req.AmountUZS, req.Provider)
	if err != nil {
		writePaymentError(w, r, h.log, err)
		return
	}

	httpx.Respond(w, http.StatusCreated, topupResponse{
		PaymentID:   pay.ID,
		Provider:    pay.Provider,
		AmountUZS:   pay.AmountUZS,
		Currency:    domain.CurrencyUZS,
		Status:      pay.Status,
		CheckoutURL: inv.CheckoutURL,
		Extra:       inv.Extra,
	})
}

type paymentStatusDTO struct {
	PaymentID string `json:"payment_id"`
	Provider  string `json:"provider"`
	AmountUZS int64  `json:"amount_uzs"`
	Status    string `json:"status"`
}

// MockConfirm подтверждает mock-платёж (dev-имитация колбэка провайдера). Только
// для провайдера mock и только во внутренней сети.
func (h *PaymentHandler) MockConfirm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	pay, _, err := h.svc.ConfirmMock(r.Context(), id)
	if err != nil {
		writePaymentError(w, r, h.log, err)
		return
	}
	httpx.Respond(w, http.StatusOK, paymentStatusDTO{
		PaymentID: pay.ID, Provider: pay.Provider, AmountUZS: pay.AmountUZS, Status: pay.Status,
	})
}

// writePaymentError переводит доменные ошибки платежей в RFC 7807.
func writePaymentError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidAmount):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_amount",
			"Некорректная сумма", "Noto'g'ri summa"))
	case errors.Is(err, domain.ErrTopupOutOfRange):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "amount_out_of_range",
			"Сумма пополнения вне допустимых границ", "To'ldirish summasi ruxsat etilgan chegaradan tashqarida"))
	case errors.Is(err, domain.ErrUnknownProvider):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "unknown_provider",
			"Неизвестный провайдер оплаты", "Noma'lum to'lov provayderi"))
	case errors.Is(err, domain.ErrPaymentNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "payment_not_found",
			"Платёж не найден", "To'lov topilmadi"))
	default:
		log.ErrorContext(r.Context(), "операция с платежом", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "payment_op_failed",
			"Не удалось выполнить операцию", "Amalni bajarib bo'lmadi"))
	}
}
