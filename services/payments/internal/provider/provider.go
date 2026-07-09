// Package provider абстрагирует платёжных провайдеров (Payme/Click/Mock). Внешняя
// часть — CreateInvoice (выставление счёта на пополнение, возвращает checkout-URL);
// входящая (колбэки провайдеров с их разными протоколами) реализована отдельными
// HTTP-обработчиками транспорта, использующими app.PaymentService.
package provider

import "context"

// Invoice — запрос на выставление счёта провайдеру.
type Invoice struct {
	PaymentID string
	UserID    string
	AmountUZS int64
	Purpose   string
}

// InvoiceResult — результат выставления счёта: ссылка оплаты и опц. поля провайдера.
type InvoiceResult struct {
	CheckoutURL string
	Extra       map[string]string
}

// Provider — платёжный провайдер (внешняя часть — выставление счёта).
type Provider interface {
	// Name возвращает код провайдера (mock/payme/click).
	Name() string
	// CreateInvoice формирует счёт на пополнение и ссылку оплаты для пользователя.
	CreateInvoice(ctx context.Context, inv Invoice) (InvoiceResult, error)
}

// Registry — набор доступных провайдеров по коду.
type Registry map[string]Provider

// NewRegistry собирает реестр провайдеров по их Name().
func NewRegistry(providers ...Provider) Registry {
	reg := make(Registry, len(providers))
	for _, p := range providers {
		reg[p.Name()] = p
	}
	return reg
}
