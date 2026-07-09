package app

import (
	"context"
	"log/slog"

	"bozor/services/payments/internal/domain"
)

// WalletStore — доступ к кошельку и леджеру (реализуется repo). Зачисление
// (Credit) выполняется при подтверждении платежа провайдером внутри repo
// (ConfirmPayment) — здесь только чтение и списание.
type WalletStore interface {
	GetWallet(ctx context.Context, userID string) (domain.Wallet, error)
	Debit(ctx context.Context, userID string, amount int64, kind string, reference *string) (domain.Wallet, error)
	ListTransactions(ctx context.Context, userID string, limit int) ([]domain.Transaction, error)
}

// WalletService — прикладной сервис кошелька (баланс, пополнение, списание, история).
type WalletService struct {
	store WalletStore
	log   *slog.Logger
}

// NewWalletService создаёт прикладной сервис кошелька.
func NewWalletService(store WalletStore, log *slog.Logger) *WalletService {
	return &WalletService{store: store, log: log}
}

// Balance возвращает текущий кошелёк пользователя (нулевой, если ещё не создан).
func (s *WalletService) Balance(ctx context.Context, userID string) (domain.Wallet, error) {
	return s.store.GetWallet(ctx, userID)
}

// Debit списывает amount с кошелька при покупке услуги (используется сагой 8.4).
// reference связывает списание с сущностью-инициатором (ad_promotion/payment).
func (s *WalletService) Debit(ctx context.Context, userID string, amount int64, kind string, reference *string) (domain.Wallet, error) {
	if amount <= 0 {
		return domain.Wallet{}, domain.ErrInvalidAmount
	}
	return s.store.Debit(ctx, userID, amount, kind, reference)
}

// Transactions возвращает историю кошелька пользователя (свежие сверху).
func (s *WalletService) Transactions(ctx context.Context, userID string, limit int) ([]domain.Transaction, error) {
	return s.store.ListTransactions(ctx, userID, limit)
}
