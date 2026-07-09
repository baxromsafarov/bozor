package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
)

type fakeWalletStore struct {
	wallet    domain.Wallet
	txs       []domain.Transaction
	credited  int64
	debited   int64
	gotKind   string
	gotRef    *string
	debitErr  error
	creditErr error
}

func (f *fakeWalletStore) GetWallet(context.Context, string) (domain.Wallet, error) {
	return f.wallet, nil
}
func (f *fakeWalletStore) Credit(_ context.Context, _ string, amount int64, kind string, ref *string) (domain.Wallet, error) {
	f.credited, f.gotKind, f.gotRef = amount, kind, ref
	if f.creditErr != nil {
		return domain.Wallet{}, f.creditErr
	}
	f.wallet.BalanceUZS += amount
	return f.wallet, nil
}
func (f *fakeWalletStore) Debit(_ context.Context, _ string, amount int64, kind string, ref *string) (domain.Wallet, error) {
	f.debited, f.gotKind, f.gotRef = amount, kind, ref
	if f.debitErr != nil {
		return domain.Wallet{}, f.debitErr
	}
	f.wallet.BalanceUZS -= amount
	return f.wallet, nil
}
func (f *fakeWalletStore) ListTransactions(context.Context, string, int) ([]domain.Transaction, error) {
	return f.txs, nil
}

// TestTopup_ValidatesAndCredits — валидная сумма зачисляется через Credit(kind=topup).
func TestTopup_ValidatesAndCredits(t *testing.T) {
	store := &fakeWalletStore{wallet: domain.Wallet{UserID: "u1", BalanceUZS: 1000}}
	svc := NewWalletService(store, discardLog())

	w, err := svc.Topup(context.Background(), "u1", 50000)
	require.NoError(t, err)
	assert.EqualValues(t, 51000, w.BalanceUZS)
	assert.EqualValues(t, 50000, store.credited)
	assert.Equal(t, domain.KindTopup, store.gotKind)
	assert.Nil(t, store.gotRef, "пополнение без reference")
}

// TestTopup_RejectsBadAmount — сумма вне границ не доходит до стора.
func TestTopup_RejectsBadAmount(t *testing.T) {
	store := &fakeWalletStore{}
	svc := NewWalletService(store, discardLog())

	_, err := svc.Topup(context.Background(), "u1", 0)
	assert.ErrorIs(t, err, domain.ErrInvalidAmount)
	_, err = svc.Topup(context.Background(), "u1", domain.TopupMax+1)
	assert.ErrorIs(t, err, domain.ErrTopupOutOfRange)
	assert.Zero(t, store.credited, "стор не вызывался при невалидной сумме")
}

// TestDebit_PassesReferenceAndKind — списание пробрасывает kind и reference.
func TestDebit_PassesReferenceAndKind(t *testing.T) {
	store := &fakeWalletStore{wallet: domain.Wallet{UserID: "u1", BalanceUZS: 100000}}
	svc := NewWalletService(store, discardLog())

	ref := "promo-1"
	w, err := svc.Debit(context.Background(), "u1", 45000, domain.KindPurchase, &ref)
	require.NoError(t, err)
	assert.EqualValues(t, 55000, w.BalanceUZS)
	assert.EqualValues(t, 45000, store.debited)
	assert.Equal(t, domain.KindPurchase, store.gotKind)
	require.NotNil(t, store.gotRef)
	assert.Equal(t, "promo-1", *store.gotRef)
}

// TestDebit_RejectsBadAmount — неположительная сумма отклоняется до стора.
func TestDebit_RejectsBadAmount(t *testing.T) {
	store := &fakeWalletStore{}
	svc := NewWalletService(store, discardLog())

	_, err := svc.Debit(context.Background(), "u1", 0, domain.KindPurchase, nil)
	assert.ErrorIs(t, err, domain.ErrInvalidAmount)
	assert.Zero(t, store.debited)
}

// TestDebit_PropagatesInsufficientFunds — ошибка недостатка средств всплывает.
func TestDebit_PropagatesInsufficientFunds(t *testing.T) {
	store := &fakeWalletStore{debitErr: domain.ErrInsufficientFunds}
	svc := NewWalletService(store, discardLog())

	_, err := svc.Debit(context.Background(), "u1", 45000, domain.KindPurchase, nil)
	assert.ErrorIs(t, err, domain.ErrInsufficientFunds)
}

// TestBalance_ReturnsWallet — баланс пробрасывается из стора.
func TestBalance_ReturnsWallet(t *testing.T) {
	store := &fakeWalletStore{wallet: domain.Wallet{UserID: "u1", BalanceUZS: 7000, Version: 3}}
	svc := NewWalletService(store, discardLog())

	w, err := svc.Balance(context.Background(), "u1")
	require.NoError(t, err)
	assert.EqualValues(t, 7000, w.BalanceUZS)
	assert.EqualValues(t, 3, w.Version)
}
