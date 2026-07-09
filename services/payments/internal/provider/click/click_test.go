package click

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

const (
	serviceID = "svc-1"
	secret    = "secret-key"
)

type fakeOps struct {
	byID     map[string]domain.Payment
	confirms int
	bound    map[string]string
}

func newOps(payments ...domain.Payment) *fakeOps {
	f := &fakeOps{byID: map[string]domain.Payment{}, bound: map[string]string{}}
	for _, p := range payments {
		f.byID[p.ID] = p
	}
	return f
}

func (f *fakeOps) ByID(_ context.Context, id string) (domain.Payment, bool, error) {
	p, ok := f.byID[id]
	return p, ok, nil
}
func (f *fakeOps) Bind(_ context.Context, id, ext string) error {
	f.bound[id] = ext
	return nil
}
func (f *fakeOps) Confirm(_ context.Context, id, _ string) (domain.Payment, bool, error) {
	f.confirms++
	p := f.byID[id]
	p.Status = domain.PaymentSucceeded
	f.byID[id] = p
	return p, true, nil
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// post отправляет form-encoded колбэк Click и возвращает разобранный ответ.
func post(t *testing.T, p *Provider, form url.Values) response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/payments/click", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestClick_PrepareComplete(t *testing.T) {
	pay := domain.Payment{ID: "p1", Purpose: domain.PurposeTopup, AmountUZS: 50000, Status: domain.PaymentPending}
	ops := newOps(pay)
	p := New(serviceID, "m1", secret, ops, discardLog())

	// Prepare с корректной подписью.
	prep := url.Values{}
	prep.Set("click_trans_id", "ct-1")
	prep.Set("service_id", serviceID)
	prep.Set("merchant_trans_id", "p1")
	prep.Set("amount", "50000.00")
	prep.Set("action", actionPrepare)
	prep.Set("sign_time", "2026-07-10 10:00:00")
	prep.Set("sign_string", sign("ct-1", serviceID, secret, "p1", "50000.00", actionPrepare, "2026-07-10 10:00:00"))

	resp := post(t, p, prep)
	require.Equal(t, errOK, resp.Error, resp.ErrorNote)
	assert.Equal(t, "ct-1", ops.bound["p1"], "click_trans_id привязан")
	prepareID := resp.MerchantPrepareID

	// Complete с корректной подписью (включает merchant_prepare_id).
	comp := url.Values{}
	comp.Set("click_trans_id", "ct-1")
	comp.Set("service_id", serviceID)
	comp.Set("merchant_trans_id", "p1")
	comp.Set("merchant_prepare_id", prepareID)
	comp.Set("amount", "50000.00")
	comp.Set("action", actionComplete)
	comp.Set("sign_time", "2026-07-10 10:01:00")
	comp.Set("sign_string", sign("ct-1", serviceID, secret, "p1", prepareID, "50000.00", actionComplete, "2026-07-10 10:01:00"))

	resp = post(t, p, comp)
	require.Equal(t, errOK, resp.Error, resp.ErrorNote)
	assert.Equal(t, "p1", resp.MerchantConfirmID)
	assert.Equal(t, 1, ops.confirms)
}

func TestClick_BadSign(t *testing.T) {
	ops := newOps(domain.Payment{ID: "p1", Purpose: domain.PurposeTopup, AmountUZS: 50000, Status: domain.PaymentPending})
	p := New(serviceID, "m1", secret, ops, discardLog())

	prep := url.Values{}
	prep.Set("click_trans_id", "ct-1")
	prep.Set("merchant_trans_id", "p1")
	prep.Set("amount", "50000.00")
	prep.Set("action", actionPrepare)
	prep.Set("sign_time", "t")
	prep.Set("sign_string", "deadbeef")

	resp := post(t, p, prep)
	assert.Equal(t, errSign, resp.Error)
	assert.Empty(t, ops.bound, "неверная подпись — без привязки")
}

func TestClick_AmountMismatch(t *testing.T) {
	ops := newOps(domain.Payment{ID: "p1", Purpose: domain.PurposeTopup, AmountUZS: 50000, Status: domain.PaymentPending})
	p := New(serviceID, "m1", secret, ops, discardLog())

	prep := url.Values{}
	prep.Set("click_trans_id", "ct-1")
	prep.Set("service_id", serviceID)
	prep.Set("merchant_trans_id", "p1")
	prep.Set("amount", "999.00")
	prep.Set("action", actionPrepare)
	prep.Set("sign_time", "t")
	prep.Set("sign_string", sign("ct-1", serviceID, secret, "p1", "999.00", actionPrepare, "t"))

	resp := post(t, p, prep)
	assert.Equal(t, errAmount, resp.Error)
}

func TestClick_UnknownOrder(t *testing.T) {
	p := New(serviceID, "m1", secret, newOps(), discardLog())

	prep := url.Values{}
	prep.Set("click_trans_id", "ct-1")
	prep.Set("service_id", serviceID)
	prep.Set("merchant_trans_id", "ghost")
	prep.Set("amount", "50000.00")
	prep.Set("action", actionPrepare)
	prep.Set("sign_time", "t")
	prep.Set("sign_string", sign("ct-1", serviceID, secret, "ghost", "50000.00", actionPrepare, "t"))

	resp := post(t, p, prep)
	assert.Equal(t, errNoOrder, resp.Error)
}

func TestClick_CreateInvoiceURL(t *testing.T) {
	p := New(serviceID, "m1", secret, newOps(), discardLog())
	inv, err := p.CreateInvoice(context.Background(), provider.Invoice{PaymentID: "p1", AmountUZS: 50000})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(inv.CheckoutURL, checkoutBase))
	assert.Contains(t, inv.CheckoutURL, "transaction_param=p1")
}
