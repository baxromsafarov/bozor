package domain

import (
	"errors"
	"time"
)

// Провайдеры оплаты.
const (
	ProviderMock  = "mock"
	ProviderPayme = "payme"
	ProviderClick = "click"
)

// Назначение платежа (в 8.3 — только пополнение кошелька).
const PurposeTopup = "topup"

// Статусы платежа.
const (
	PaymentPending   = "pending"
	PaymentSucceeded = "succeeded"
	PaymentFailed    = "failed"
	PaymentCanceled  = "canceled"
)

// Ошибки платежей.
var (
	ErrPaymentNotFound = errors.New("payments: платёж не найден")
	ErrUnknownProvider = errors.New("payments: неизвестный провайдер оплаты")
	ErrAmountMismatch  = errors.New("payments: сумма не совпадает")
	ErrPaymentState    = errors.New("payments: недопустимое состояние платежа")
)

// Payment — платёж через провайдера (пополнение кошелька).
type Payment struct {
	ID         string
	UserID     string
	Provider   string
	Purpose    string
	AmountUZS  int64
	Status     string
	ExternalID *string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// IsTerminal сообщает, завершён ли платёж (успех/отказ/отмена — колбэк повторный).
func (p Payment) IsTerminal() bool {
	return p.Status == PaymentSucceeded || p.Status == PaymentFailed || p.Status == PaymentCanceled
}

// ValidateProvider проверяет, что провайдер поддерживается.
func ValidateProvider(provider string) error {
	switch provider {
	case ProviderMock, ProviderPayme, ProviderClick:
		return nil
	default:
		return ErrUnknownProvider
	}
}
