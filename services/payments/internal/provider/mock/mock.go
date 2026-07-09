// Package mock — платёжный провайдер для dev/тестов: счёт указывает на внутренний
// эндпоинт подтверждения, который вызывается вручную (имитация колбэка провайдера).
package mock

import (
	"context"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

// Provider — mock-провайдер.
type Provider struct{}

// New создаёт mock-провайдер.
func New() *Provider { return &Provider{} }

// Name возвращает код провайдера.
func (p *Provider) Name() string { return domain.ProviderMock }

// CreateInvoice возвращает ссылку на внутренний эндпоинт подтверждения (в проде
// провайдер редиректит пользователя на свою страницу оплаты).
func (p *Provider) CreateInvoice(_ context.Context, inv provider.Invoice) (provider.InvoiceResult, error) {
	confirm := "/internal/payments/mock/" + inv.PaymentID + "/confirm"
	return provider.InvoiceResult{
		CheckoutURL: "mock://pay/" + inv.PaymentID,
		Extra:       map[string]string{"confirm_endpoint": confirm},
	}, nil
}
