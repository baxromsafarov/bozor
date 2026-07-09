package payme

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

const merchantKey = "secret-key"

// fakeOps — in-memory реализация PaymentOps.
type fakeOps struct {
	byID     map[string]domain.Payment
	byExt    map[string]domain.Payment
	confirms int
	cancels  int
}

func newOps(payments ...domain.Payment) *fakeOps {
	f := &fakeOps{byID: map[string]domain.Payment{}, byExt: map[string]domain.Payment{}}
	for _, p := range payments {
		f.byID[p.ID] = p
		if p.ExternalID != nil {
			f.byExt[*p.ExternalID] = p
		}
	}
	return f
}

func (f *fakeOps) ByID(_ context.Context, id string) (domain.Payment, bool, error) {
	p, ok := f.byID[id]
	return p, ok, nil
}
func (f *fakeOps) ByExternal(_ context.Context, ext string) (domain.Payment, bool, error) {
	p, ok := f.byExt[ext]
	return p, ok, nil
}
func (f *fakeOps) Bind(_ context.Context, id, ext string) error {
	p := f.byID[id]
	p.ExternalID = &ext
	f.byID[id] = p
	f.byExt[ext] = p
	return nil
}
func (f *fakeOps) Confirm(_ context.Context, id, _ string) (domain.Payment, bool, error) {
	f.confirms++
	p := f.byID[id]
	p.Status = domain.PaymentSucceeded
	p.UpdatedAt = time.Unix(100, 0)
	f.byID[id] = p
	return p, true, nil
}
func (f *fakeOps) Cancel(_ context.Context, id string) (domain.Payment, bool, error) {
	f.cancels++
	p := f.byID[id]
	p.Status = domain.PaymentCanceled
	f.byID[id] = p
	return p, true, nil
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// rpc отправляет JSON-RPC запрос в обработчик Payme и возвращает разобранный ответ.
func rpc(t *testing.T, p *Provider, key, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/payments/payme", strings.NewReader(body))
	if key != "" {
		req.SetBasicAuth("Paycom", key)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func errorCode(resp map[string]any) (float64, bool) {
	e, ok := resp["error"].(map[string]any)
	if !ok {
		return 0, false
	}
	return e["code"].(float64), true
}

func result(resp map[string]any) map[string]any {
	r, _ := resp["result"].(map[string]any)
	return r
}

func TestPayme_Auth(t *testing.T) {
	p := New("m1", merchantKey, newOps(), discardLog())
	resp := rpc(t, p, "wrong-key", `{"id":1,"method":"CheckPerformTransaction","params":{}}`)
	code, ok := errorCode(resp)
	require.True(t, ok)
	assert.EqualValues(t, errAuth, code)
}

func TestPayme_CheckPerform(t *testing.T) {
	pay := domain.Payment{ID: "p1", Purpose: domain.PurposeTopup, AmountUZS: 50000, Status: domain.PaymentPending}
	p := New("m1", merchantKey, newOps(pay), discardLog())

	// Совпадение суммы (в тийинах) → allow.
	resp := rpc(t, p, merchantKey, `{"id":1,"method":"CheckPerformTransaction","params":{"amount":5000000,"account":{"payment_id":"p1"}}}`)
	assert.Equal(t, true, result(resp)["allow"])

	// Неверная сумма → -31001.
	resp = rpc(t, p, merchantKey, `{"id":1,"method":"CheckPerformTransaction","params":{"amount":9999,"account":{"payment_id":"p1"}}}`)
	code, _ := errorCode(resp)
	assert.EqualValues(t, errAmount, code)

	// Нет заказа → -31050.
	resp = rpc(t, p, merchantKey, `{"id":1,"method":"CheckPerformTransaction","params":{"amount":5000000,"account":{"payment_id":"nope"}}}`)
	code, _ = errorCode(resp)
	assert.EqualValues(t, errAccount, code)
}

func TestPayme_CreatePerformCancel(t *testing.T) {
	pay := domain.Payment{ID: "p1", Purpose: domain.PurposeTopup, AmountUZS: 50000, Status: domain.PaymentPending,
		CreatedAt: time.Unix(10, 0)}
	ops := newOps(pay)
	p := New("m1", merchantKey, ops, discardLog())

	// CreateTransaction привязывает транзакцию Payme, state=1.
	resp := rpc(t, p, merchantKey, `{"id":1,"method":"CreateTransaction","params":{"id":"payme-tx-1","time":123,"amount":5000000,"account":{"payment_id":"p1"}}}`)
	assert.EqualValues(t, stateCreated, result(resp)["state"])
	assert.Equal(t, "p1", result(resp)["transaction"])

	// PerformTransaction по id транзакции Payme → зачисление, state=2.
	resp = rpc(t, p, merchantKey, `{"id":1,"method":"PerformTransaction","params":{"id":"payme-tx-1"}}`)
	assert.EqualValues(t, statePerformed, result(resp)["state"])
	assert.Equal(t, 1, ops.confirms, "перевод выполнен один раз")

	// Повторный Perform (провайдер ретраит) → снова state=2, но без второго Confirm-эффекта
	// (идемпотентность обеспечивает repo.ConfirmPayment; здесь проверяем, что вызов не падает).
	resp = rpc(t, p, merchantKey, `{"id":1,"method":"PerformTransaction","params":{"id":"payme-tx-1"}}`)
	assert.EqualValues(t, statePerformed, result(resp)["state"])
}

func TestPayme_PerformUnknownTransaction(t *testing.T) {
	p := New("m1", merchantKey, newOps(), discardLog())
	resp := rpc(t, p, merchantKey, `{"id":1,"method":"PerformTransaction","params":{"id":"ghost"}}`)
	code, _ := errorCode(resp)
	assert.EqualValues(t, errTxnNotFound, code)
}

func TestPayme_CancelTransaction(t *testing.T) {
	ext := "payme-tx-1"
	pay := domain.Payment{ID: "p1", Purpose: domain.PurposeTopup, AmountUZS: 50000, Status: domain.PaymentPending, ExternalID: &ext}
	ops := newOps(pay)
	p := New("m1", merchantKey, ops, discardLog())

	resp := rpc(t, p, merchantKey, `{"id":1,"method":"CancelTransaction","params":{"id":"payme-tx-1","reason":3}}`)
	assert.EqualValues(t, stateCanceled, result(resp)["state"])
	assert.Equal(t, 1, ops.cancels)
}

func TestPayme_CheckTransaction(t *testing.T) {
	ext := "payme-tx-1"
	pay := domain.Payment{ID: "p1", Purpose: domain.PurposeTopup, AmountUZS: 50000, Status: domain.PaymentSucceeded,
		ExternalID: &ext, CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(20, 0)}
	p := New("m1", merchantKey, newOps(pay), discardLog())

	resp := rpc(t, p, merchantKey, `{"id":1,"method":"CheckTransaction","params":{"id":"payme-tx-1"}}`)
	assert.EqualValues(t, statePerformed, result(resp)["state"])
}

func TestPayme_UnknownMethod(t *testing.T) {
	p := New("m1", merchantKey, newOps(), discardLog())
	resp := rpc(t, p, merchantKey, `{"id":1,"method":"Nope","params":{}}`)
	code, _ := errorCode(resp)
	assert.EqualValues(t, errMethod, code)
}

func TestPayme_CreateInvoiceURL(t *testing.T) {
	p := New("merchant-42", merchantKey, newOps(), discardLog())
	inv, err := p.CreateInvoice(context.Background(), provider.Invoice{PaymentID: "p1", AmountUZS: 50000})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(inv.CheckoutURL, checkoutBase))
}
