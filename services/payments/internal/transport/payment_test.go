package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

type fakePayments struct {
	initErr      error
	confirmErr   error
	gotAmount    int64
	gotProvider  string
	gotConfirmID string
}

func (f *fakePayments) InitTopup(_ context.Context, _ string, amount int64, providerName string) (domain.Payment, provider.InvoiceResult, error) {
	f.gotAmount, f.gotProvider = amount, providerName
	if f.initErr != nil {
		return domain.Payment{}, provider.InvoiceResult{}, f.initErr
	}
	return domain.Payment{ID: "p1", Provider: providerName, AmountUZS: amount, Status: domain.PaymentPending},
		provider.InvoiceResult{CheckoutURL: "mock://pay/p1", Extra: map[string]string{"confirm_endpoint": "/internal/payments/mock/p1/confirm"}}, nil
}
func (f *fakePayments) GetPayment(context.Context, string) (domain.Payment, bool, error) {
	return domain.Payment{}, false, nil
}
func (f *fakePayments) ConfirmMock(_ context.Context, id string) (domain.Payment, bool, error) {
	f.gotConfirmID = id
	if f.confirmErr != nil {
		return domain.Payment{}, false, f.confirmErr
	}
	return domain.Payment{ID: id, Provider: domain.ProviderMock, AmountUZS: 50000, Status: domain.PaymentSucceeded}, true, nil
}

func paymentServer(fp *fakePayments, userID string) http.Handler {
	router := NewRouter(Deps{Log: discardLog(), Payments: NewPaymentHandler(fp, discardLog())})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if userID != "" {
			req.Header.Set("X-User-Id", userID)
		}
		router.ServeHTTP(w, req)
	})
}

func TestTopup_201_DefaultsMock(t *testing.T) {
	fp := &fakePayments{}
	rec := do(t, paymentServer(fp, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":50000}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.EqualValues(t, 50000, fp.gotAmount)
	assert.Equal(t, domain.ProviderMock, fp.gotProvider, "провайдер по умолчанию — mock")

	var body topupResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "p1", body.PaymentID)
	assert.Equal(t, domain.PaymentPending, body.Status)
	assert.Equal(t, "mock://pay/p1", body.CheckoutURL)
	assert.Equal(t, "UZS", body.Currency)
}

func TestTopup_ExplicitProvider(t *testing.T) {
	fp := &fakePayments{}
	rec := do(t, paymentServer(fp, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":50000,"provider":"payme"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, domain.ProviderPayme, fp.gotProvider)
}

func TestTopup_Anonymous_401(t *testing.T) {
	rec := do(t, paymentServer(&fakePayments{}, ""), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":50000}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTopup_InvalidAmount_422(t *testing.T) {
	fp := &fakePayments{initErr: domain.ErrInvalidAmount}
	rec := do(t, paymentServer(fp, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":0}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestTopup_OutOfRange_422(t *testing.T) {
	fp := &fakePayments{initErr: domain.ErrTopupOutOfRange}
	rec := do(t, paymentServer(fp, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":99999999999}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestTopup_UnknownProvider_422(t *testing.T) {
	fp := &fakePayments{initErr: domain.ErrUnknownProvider}
	rec := do(t, paymentServer(fp, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":50000,"provider":"paypal"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestTopup_BadJSON_422(t *testing.T) {
	rec := do(t, paymentServer(&fakePayments{}, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestMockConfirm_200(t *testing.T) {
	fp := &fakePayments{}
	// Внутренний колбэк — без пользовательской авторизации.
	rec := do(t, paymentServer(fp, ""), http.MethodPost, "/internal/payments/mock/p1/confirm", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "p1", fp.gotConfirmID)

	var body paymentStatusDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, domain.PaymentSucceeded, body.Status)
}

func TestMockConfirm_NotFound_404(t *testing.T) {
	fp := &fakePayments{confirmErr: domain.ErrPaymentNotFound}
	rec := do(t, paymentServer(fp, ""), http.MethodPost, "/internal/payments/mock/x/confirm", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
