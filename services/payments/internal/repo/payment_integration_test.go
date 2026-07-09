//go:build integration

package repo_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/repo"
)

func newPayment(t *testing.T, provider string, amount int64) domain.Payment {
	t.Helper()
	return domain.Payment{
		ID: uuid.NewString(), UserID: uuid.NewString(), Provider: provider,
		Purpose: domain.PurposeTopup, AmountUZS: amount, Status: domain.PaymentPending,
	}
}

// TestConfirmPayment_CreditsOnceAndEmitsEvent — подтверждение зачисляет средства,
// кладёт bozor.payment.succeeded в outbox; повторный колбэк не зачисляет дважды.
func TestConfirmPayment_CreditsOnceAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	pay := newPayment(t, domain.ProviderMock, 50000)
	require.NoError(t, r.CreatePayment(ctx, pay))

	// Первое подтверждение: переход произошёл, средства зачислены.
	got, credited, err := r.ConfirmPayment(ctx, pay.ID, "mock-"+pay.ID)
	require.NoError(t, err)
	assert.True(t, credited, "первый колбэк зачисляет")
	assert.Equal(t, domain.PaymentSucceeded, got.Status)

	w, err := r.GetWallet(ctx, pay.UserID)
	require.NoError(t, err)
	assert.EqualValues(t, 50000, w.BalanceUZS)

	// Событие в outbox ровно одно.
	var outboxN int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectPaymentSucceeded).Scan(&outboxN))
	assert.Equal(t, 1, outboxN)

	// Повторный колбэк (провайдер ретраит) — без второго зачисления и события.
	_, credited2, err := r.ConfirmPayment(ctx, pay.ID, "mock-"+pay.ID)
	require.NoError(t, err)
	assert.False(t, credited2, "повторный колбэк не зачисляет")

	w, err = r.GetWallet(ctx, pay.UserID)
	require.NoError(t, err)
	assert.EqualValues(t, 50000, w.BalanceUZS, "баланс не удвоился")
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectPaymentSucceeded).Scan(&outboxN))
	assert.Equal(t, 1, outboxN, "второе событие не создано")
}

// TestFailPayment_EmitsFailedEvent — отмена/провал переводит платёж и публикует
// bozor.payment.failed; идемпотентно.
func TestFailPayment_EmitsFailedEvent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	pay := newPayment(t, domain.ProviderClick, 30000)
	require.NoError(t, r.CreatePayment(ctx, pay))

	got, changed, err := r.FailPayment(ctx, pay.ID, domain.PaymentCanceled)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, domain.PaymentCanceled, got.Status)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectPaymentFailed).Scan(&n))
	assert.Equal(t, 1, n)

	// Повтор — без нового события.
	_, changed2, err := r.FailPayment(ctx, pay.ID, domain.PaymentCanceled)
	require.NoError(t, err)
	assert.False(t, changed2)

	// Кошелёк не создавался (провал не зачисляет).
	w, err := r.GetWallet(ctx, pay.UserID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, w.BalanceUZS)
}

// TestSetExternalID_AndLookup — привязка external_id и поиск по (provider, external).
func TestSetExternalID_AndLookup(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	pay := newPayment(t, domain.ProviderPayme, 90000)
	require.NoError(t, r.CreatePayment(ctx, pay))
	require.NoError(t, r.SetExternalID(ctx, pay.ID, "payme-tx-9"))
	// Повторная привязка того же external_id — не ошибка.
	require.NoError(t, r.SetExternalID(ctx, pay.ID, "payme-tx-9"))

	got, found, err := r.GetPaymentByExternal(ctx, domain.ProviderPayme, "payme-tx-9")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, pay.ID, got.ID)
	require.NotNil(t, got.ExternalID)
	assert.Equal(t, "payme-tx-9", *got.ExternalID)
}

// TestConfirmPayment_NotFound — подтверждение несуществующего платежа → ошибка.
func TestConfirmPayment_NotFound(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))

	_, _, err := r.ConfirmPayment(ctx, uuid.NewString(), "ext")
	assert.ErrorIs(t, err, domain.ErrPaymentNotFound)
}
