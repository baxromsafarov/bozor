package app

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

// PaymentStore — доступ к платежам (реализуется repo).
type PaymentStore interface {
	CreatePayment(ctx context.Context, p domain.Payment) error
	GetPayment(ctx context.Context, id string) (domain.Payment, bool, error)
	GetPaymentByExternal(ctx context.Context, provider, externalID string) (domain.Payment, bool, error)
	SetExternalID(ctx context.Context, id, externalID string) error
	ConfirmPayment(ctx context.Context, id, externalID string) (domain.Payment, bool, error)
	FailPayment(ctx context.Context, id, status string) (domain.Payment, bool, error)
}

// PaymentService — инициация пополнения через провайдера и операции над платежами
// для колбэков провайдеров.
type PaymentService struct {
	store     PaymentStore
	providers provider.Registry
	log       *slog.Logger
}

// NewPaymentService создаёт сервис платежей.
func NewPaymentService(store PaymentStore, providers provider.Registry, log *slog.Logger) *PaymentService {
	return &PaymentService{store: store, providers: providers, log: log}
}

// InitTopup создаёт платёж (pending) и выставляет счёт у выбранного провайдера,
// возвращая платёж и ссылку оплаты. Средства зачислятся на кошелёк после
// подтверждения провайдером (колбэк). Валидирует сумму и провайдера.
func (s *PaymentService) InitTopup(ctx context.Context, userID string, amount int64, providerName string) (domain.Payment, provider.InvoiceResult, error) {
	if err := domain.ValidateTopup(amount); err != nil {
		return domain.Payment{}, provider.InvoiceResult{}, err
	}
	prov, ok := s.providers[providerName]
	if !ok {
		return domain.Payment{}, provider.InvoiceResult{}, domain.ErrUnknownProvider
	}

	p := domain.Payment{
		ID:        uuid.New().String(),
		UserID:    userID,
		Provider:  providerName,
		Purpose:   domain.PurposeTopup,
		AmountUZS: amount,
		Status:    domain.PaymentPending,
	}
	if err := s.store.CreatePayment(ctx, p); err != nil {
		return domain.Payment{}, provider.InvoiceResult{}, err
	}
	inv, err := prov.CreateInvoice(ctx, provider.Invoice{
		PaymentID: p.ID, UserID: userID, AmountUZS: amount, Purpose: domain.PurposeTopup,
	})
	if err != nil {
		return domain.Payment{}, provider.InvoiceResult{}, err
	}
	return p, inv, nil
}

// GetPayment возвращает платёж по id.
func (s *PaymentService) GetPayment(ctx context.Context, id string) (domain.Payment, bool, error) {
	return s.store.GetPayment(ctx, id)
}

// ConfirmMock подтверждает mock-платёж (dev-имитация колбэка провайдера).
// Работает только для платежей провайдера mock; external_id — производный от id.
func (s *PaymentService) ConfirmMock(ctx context.Context, id string) (domain.Payment, bool, error) {
	pay, found, err := s.store.GetPayment(ctx, id)
	if err != nil {
		return domain.Payment{}, false, err
	}
	if !found || pay.Provider != domain.ProviderMock {
		return domain.Payment{}, false, domain.ErrPaymentNotFound
	}
	return s.store.ConfirmPayment(ctx, id, "mock-"+id)
}

// ProviderOps — операции над платежами в контексте одного провайдера (для колбэков —
// поиск по external_id идёт в пределах этого провайдера). Реализует PaymentOps
// пакетов payme/click.
type ProviderOps struct {
	store    PaymentStore
	provider string
}

// NewProviderOps создаёт операции над платежами, привязанные к провайдеру.
func NewProviderOps(store PaymentStore, providerName string) ProviderOps {
	return ProviderOps{store: store, provider: providerName}
}

// ByID возвращает платёж по нашему id.
func (o ProviderOps) ByID(ctx context.Context, id string) (domain.Payment, bool, error) {
	return o.store.GetPayment(ctx, id)
}

// ByExternal возвращает платёж по id транзакции провайдера.
func (o ProviderOps) ByExternal(ctx context.Context, externalID string) (domain.Payment, bool, error) {
	return o.store.GetPaymentByExternal(ctx, o.provider, externalID)
}

// Bind привязывает id транзакции провайдера к платежу.
func (o ProviderOps) Bind(ctx context.Context, id, externalID string) error {
	return o.store.SetExternalID(ctx, id, externalID)
}

// Confirm подтверждает платёж (идемпотентно зачисляет средства).
func (o ProviderOps) Confirm(ctx context.Context, id, externalID string) (domain.Payment, bool, error) {
	return o.store.ConfirmPayment(ctx, id, externalID)
}

// Cancel отменяет платёж (переводит в canceled).
func (o ProviderOps) Cancel(ctx context.Context, id string) (domain.Payment, bool, error) {
	return o.store.FailPayment(ctx, id, domain.PaymentCanceled)
}
