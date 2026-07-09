package domain

import (
	"errors"
	"time"
)

// Виды операций леджера.
const (
	KindTopup    = "topup"    // пополнение кошелька
	KindPurchase = "purchase" // списание при покупке услуги
	KindRefund   = "refund"   // возврат неиспользованных дней
)

// Направления проводки.
const (
	DirectionDebit  = "debit"  // уменьшает счёт
	DirectionCredit = "credit" // увеличивает счёт
)

// Счета леджера. user_wallet — счёт пользователя; остальные — системные
// (контрагенты парных проводок).
const (
	AccountUserWallet    = "user_wallet"
	AccountExternalTopup = "external_topup"    // источник средств при пополнении
	AccountRevenue       = "promotion_revenue" // выручка при покупке услуги
	AccountRefundSource  = "refund_source"     // источник возврата
)

// Границы разового пополнения (UZS).
const (
	TopupMin int64 = 1000
	TopupMax int64 = 50_000_000
)

// Ошибки кошелька.
var (
	ErrInvalidAmount     = errors.New("payments: некорректная сумма")
	ErrTopupOutOfRange   = errors.New("payments: сумма пополнения вне допустимых границ")
	ErrInsufficientFunds = errors.New("payments: недостаточно средств")
)

// Wallet — кошелёк пользователя (баланс + версия для оптимистичной конкуренции).
type Wallet struct {
	UserID     string
	BalanceUZS int64
	Version    int64
}

// Transaction — проводка счёта пользователя (представление истории кошелька).
type Transaction struct {
	ID          string
	OperationID string
	Kind        string
	Direction   string
	AmountUZS   int64
	Reference   *string
	CreatedAt   time.Time
}

// SignedAmount — сумма со знаком относительно баланса пользователя: пополнение
// (credit) увеличивает (+), списание (debit) уменьшает (−).
func (t Transaction) SignedAmount() int64 {
	if t.Direction == DirectionDebit {
		return -t.AmountUZS
	}
	return t.AmountUZS
}

// ValidateTopup проверяет сумму пополнения: положительная и в допустимых границах.
func ValidateTopup(amount int64) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if amount < TopupMin || amount > TopupMax {
		return ErrTopupOutOfRange
	}
	return nil
}
