package transport

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/payments/internal/domain"
)

// transactions clamp по умолчанию/максимум для истории кошелька.
const (
	defaultTxLimit = 20
	maxTxLimit     = 100
)

// Wallet — use-cases кошелька (реализуется app.WalletService).
type Wallet interface {
	Balance(ctx context.Context, userID string) (domain.Wallet, error)
	Transactions(ctx context.Context, userID string, limit int) ([]domain.Transaction, error)
}

// WalletHandler обслуживает эндпоинты кошелька пользователя.
type WalletHandler struct {
	svc Wallet
	log *slog.Logger
}

// NewWalletHandler создаёт обработчик кошелька.
func NewWalletHandler(svc Wallet, log *slog.Logger) *WalletHandler {
	return &WalletHandler{svc: svc, log: log}
}

type walletDTO struct {
	UserID     string           `json:"user_id"`
	BalanceUZS int64            `json:"balance_uzs"`
	Currency   string           `json:"currency"`
	Version    int64            `json:"version"`
	Recent     []transactionDTO `json:"recent_transactions"`
}

type transactionDTO struct {
	ID           string    `json:"id"`
	OperationID  string    `json:"operation_id"`
	Kind         string    `json:"kind"`
	Direction    string    `json:"direction"`
	AmountUZS    int64     `json:"amount_uzs"`
	SignedAmount int64     `json:"signed_amount_uzs"`
	Reference    *string   `json:"reference"`
	CreatedAt    time.Time `json:"created_at"`
}

// Get отдаёт кошелёк текущего пользователя с последними проводками.
func (h *WalletHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID := authx.UserID(r.Context())

	wallet, err := h.svc.Balance(r.Context(), userID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "чтение кошелька", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "wallet_failed",
			"Не удалось получить кошелёк", "Hamyonni olib bo'lmadi"))
		return
	}
	recent, err := h.svc.Transactions(r.Context(), userID, defaultTxLimit)
	if err != nil {
		h.log.ErrorContext(r.Context(), "чтение истории кошелька", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "wallet_failed",
			"Не удалось получить кошелёк", "Hamyonni olib bo'lmadi"))
		return
	}

	httpx.Respond(w, http.StatusOK, toWalletDTO(wallet, recent))
}

// Transactions отдаёт историю кошелька (свежие сверху) с пагинацией по limit.
func (h *WalletHandler) Transactions(w http.ResponseWriter, r *http.Request) {
	userID := authx.UserID(r.Context())
	limit := clampLimit(r.URL.Query().Get("limit"), defaultTxLimit, maxTxLimit)

	txs, err := h.svc.Transactions(r.Context(), userID, limit)
	if err != nil {
		h.log.ErrorContext(r.Context(), "история кошелька", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "transactions_failed",
			"Не удалось получить историю", "Tarixni olib bo'lmadi"))
		return
	}

	out := make([]transactionDTO, len(txs))
	for i, t := range txs {
		out[i] = toTransactionDTO(t)
	}
	httpx.Respond(w, http.StatusOK, map[string]any{"transactions": out})
}

func toWalletDTO(w domain.Wallet, recent []domain.Transaction) walletDTO {
	dto := walletDTO{
		UserID:     w.UserID,
		BalanceUZS: w.BalanceUZS,
		Currency:   domain.CurrencyUZS,
		Version:    w.Version,
		Recent:     make([]transactionDTO, len(recent)),
	}
	for i, t := range recent {
		dto.Recent[i] = toTransactionDTO(t)
	}
	return dto
}

func toTransactionDTO(t domain.Transaction) transactionDTO {
	return transactionDTO{
		ID: t.ID, OperationID: t.OperationID, Kind: t.Kind, Direction: t.Direction,
		AmountUZS: t.AmountUZS, SignedAmount: t.SignedAmount(), Reference: t.Reference,
		CreatedAt: t.CreatedAt,
	}
}

// clampLimit разбирает limit из query, применяя дефолт и потолок.
func clampLimit(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}
