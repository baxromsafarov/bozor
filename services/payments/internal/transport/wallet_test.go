package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
)

type fakeWallet struct {
	wallet     domain.Wallet
	txs        []domain.Transaction
	topupErr   error
	gotTopup   int64
	gotUserID  string
	balanceErr error
}

func (f *fakeWallet) Balance(_ context.Context, userID string) (domain.Wallet, error) {
	f.gotUserID = userID
	if f.balanceErr != nil {
		return domain.Wallet{}, f.balanceErr
	}
	return f.wallet, nil
}
func (f *fakeWallet) Topup(_ context.Context, _ string, amount int64) (domain.Wallet, error) {
	f.gotTopup = amount
	if f.topupErr != nil {
		return domain.Wallet{}, f.topupErr
	}
	f.wallet.BalanceUZS += amount
	return f.wallet, nil
}
func (f *fakeWallet) Transactions(context.Context, string, int) ([]domain.Transaction, error) {
	return f.txs, nil
}

// walletServer собирает роутер и проставляет проброшенный заголовок идентичности.
func walletServer(fw *fakeWallet, userID string) http.Handler {
	router := NewRouter(Deps{Log: discardLog(), Wallet: NewWalletHandler(fw, discardLog())})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if userID != "" {
			req.Header.Set("X-User-Id", userID)
		}
		router.ServeHTTP(w, req)
	})
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestWallet_Get_OK(t *testing.T) {
	fw := &fakeWallet{
		wallet: domain.Wallet{UserID: "u1", BalanceUZS: 12000, Version: 2},
		txs:    []domain.Transaction{{ID: "t1", Kind: domain.KindTopup, Direction: domain.DirectionCredit, AmountUZS: 12000, CreatedAt: time.Unix(1, 0).UTC()}},
	}
	rec := do(t, walletServer(fw, "u1"), http.MethodGet, "/api/v1/me/wallet", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var body walletDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "u1", body.UserID)
	assert.EqualValues(t, 12000, body.BalanceUZS)
	assert.Equal(t, "UZS", body.Currency)
	require.Len(t, body.Recent, 1)
	assert.EqualValues(t, 12000, body.Recent[0].SignedAmount, "пополнение — положительная сумма")
	assert.Equal(t, "u1", fw.gotUserID)
}

func TestWallet_Get_Anonymous_401(t *testing.T) {
	rec := do(t, walletServer(&fakeWallet{}, ""), http.MethodGet, "/api/v1/me/wallet", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWallet_Topup_OK(t *testing.T) {
	fw := &fakeWallet{wallet: domain.Wallet{UserID: "u1", BalanceUZS: 1000}}
	rec := do(t, walletServer(fw, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":50000}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.EqualValues(t, 50000, fw.gotTopup)

	var body walletDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.EqualValues(t, 51000, body.BalanceUZS)
}

func TestWallet_Topup_Anonymous_401(t *testing.T) {
	rec := do(t, walletServer(&fakeWallet{}, ""), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":50000}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWallet_Topup_InvalidAmount_422(t *testing.T) {
	fw := &fakeWallet{topupErr: domain.ErrInvalidAmount}
	rec := do(t, walletServer(fw, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":0}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestWallet_Topup_OutOfRange_422(t *testing.T) {
	fw := &fakeWallet{topupErr: domain.ErrTopupOutOfRange}
	rec := do(t, walletServer(fw, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{"amount_uzs":99999999999}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestWallet_Topup_BadJSON_422(t *testing.T) {
	rec := do(t, walletServer(&fakeWallet{}, "u1"), http.MethodPost, "/api/v1/wallet/topup", `{`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestWallet_Transactions_OK(t *testing.T) {
	fw := &fakeWallet{txs: []domain.Transaction{
		{ID: "t1", Kind: domain.KindTopup, Direction: domain.DirectionCredit, AmountUZS: 50000, CreatedAt: time.Unix(2, 0).UTC()},
		{ID: "t2", Kind: domain.KindPurchase, Direction: domain.DirectionDebit, AmountUZS: 30000, CreatedAt: time.Unix(1, 0).UTC()},
	}}
	rec := do(t, walletServer(fw, "u1"), http.MethodGet, "/api/v1/me/wallet/transactions", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"transactions"`)
	assert.Contains(t, rec.Body.String(), `"signed_amount_uzs":-30000`)
}
