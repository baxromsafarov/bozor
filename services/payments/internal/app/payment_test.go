package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

type fakePaymentStore struct {
	created    *domain.Payment
	payment    domain.Payment
	found      bool
	confirmErr error
	confirmed  bool
	byExternal domain.Payment
	extFound   bool
}

func (f *fakePaymentStore) CreatePayment(_ context.Context, p domain.Payment) error {
	f.created = &p
	return nil
}
func (f *fakePaymentStore) GetPayment(context.Context, string) (domain.Payment, bool, error) {
	return f.payment, f.found, nil
}
func (f *fakePaymentStore) GetPaymentByExternal(context.Context, string, string) (domain.Payment, bool, error) {
	return f.byExternal, f.extFound, nil
}
func (f *fakePaymentStore) SetExternalID(context.Context, string, string) error { return nil }
func (f *fakePaymentStore) ConfirmPayment(_ context.Context, _, _ string) (domain.Payment, bool, error) {
	if f.confirmErr != nil {
		return domain.Payment{}, false, f.confirmErr
	}
	f.payment.Status = domain.PaymentSucceeded
	return f.payment, f.confirmed, nil
}
func (f *fakePaymentStore) FailPayment(_ context.Context, _, status string) (domain.Payment, bool, error) {
	f.payment.Status = status
	return f.payment, true, nil
}

type stubProvider struct {
	name   string
	gotInv provider.Invoice
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) CreateInvoice(_ context.Context, inv provider.Invoice) (provider.InvoiceResult, error) {
	s.gotInv = inv
	return provider.InvoiceResult{CheckoutURL: "checkout://" + inv.PaymentID}, nil
}

// TestInitTopup_CreatesPaymentAndInvoice — валидный топап создаёт pending-платёж
// и выставляет счёт у выбранного провайдера.
func TestInitTopup_CreatesPaymentAndInvoice(t *testing.T) {
	store := &fakePaymentStore{}
	prov := &stubProvider{name: domain.ProviderMock}
	svc := NewPaymentService(store, provider.NewRegistry(prov), discardLog())

	pay, inv, err := svc.InitTopup(context.Background(), "u1", 50000, domain.ProviderMock)
	require.NoError(t, err)
	require.NotNil(t, store.created)
	assert.Equal(t, "u1", store.created.UserID)
	assert.Equal(t, domain.PaymentPending, store.created.Status)
	assert.Equal(t, domain.PurposeTopup, store.created.Purpose)
	assert.EqualValues(t, 50000, store.created.AmountUZS)
	assert.Equal(t, pay.ID, prov.gotInv.PaymentID, "провайдер получил id платежа")
	assert.Equal(t, "checkout://"+pay.ID, inv.CheckoutURL)
}

// TestInitTopup_RejectsBadAmount — сумма вне границ не создаёт платёж.
func TestInitTopup_RejectsBadAmount(t *testing.T) {
	store := &fakePaymentStore{}
	svc := NewPaymentService(store, provider.NewRegistry(&stubProvider{name: domain.ProviderMock}), discardLog())

	_, _, err := svc.InitTopup(context.Background(), "u1", 0, domain.ProviderMock)
	assert.ErrorIs(t, err, domain.ErrInvalidAmount)
	assert.Nil(t, store.created, "платёж не создан")
}

// TestInitTopup_UnknownProvider — неизвестный провайдер отклоняется.
func TestInitTopup_UnknownProvider(t *testing.T) {
	store := &fakePaymentStore{}
	svc := NewPaymentService(store, provider.NewRegistry(&stubProvider{name: domain.ProviderMock}), discardLog())

	_, _, err := svc.InitTopup(context.Background(), "u1", 50000, "paypal")
	assert.ErrorIs(t, err, domain.ErrUnknownProvider)
	assert.Nil(t, store.created)
}

// TestConfirmMock_OnlyMockProvider — подтверждать mock-колбэком можно только
// mock-платёж; payme/click так не подтверждаются.
func TestConfirmMock_OnlyMockProvider(t *testing.T) {
	store := &fakePaymentStore{payment: domain.Payment{ID: "p1", Provider: domain.ProviderPayme}, found: true}
	svc := NewPaymentService(store, provider.Registry{}, discardLog())

	_, _, err := svc.ConfirmMock(context.Background(), "p1")
	assert.ErrorIs(t, err, domain.ErrPaymentNotFound, "не mock — нельзя")

	store2 := &fakePaymentStore{payment: domain.Payment{ID: "p1", Provider: domain.ProviderMock}, found: true, confirmed: true}
	svc2 := NewPaymentService(store2, provider.Registry{}, discardLog())
	pay, credited, err := svc2.ConfirmMock(context.Background(), "p1")
	require.NoError(t, err)
	assert.True(t, credited)
	assert.Equal(t, domain.PaymentSucceeded, pay.Status)
}

// TestProviderOps_ScopesByProvider — ByExternal ищет платёж в пределах провайдера.
func TestProviderOps_Delegates(t *testing.T) {
	store := &fakePaymentStore{byExternal: domain.Payment{ID: "p1"}, extFound: true}
	ops := NewProviderOps(store, domain.ProviderPayme)

	p, found, err := ops.ByExternal(context.Background(), "ext-1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "p1", p.ID)

	pay, _, err := ops.Cancel(context.Background(), "p1")
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentCanceled, pay.Status)
}
