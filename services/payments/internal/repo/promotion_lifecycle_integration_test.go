//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/repo"
)

// seedTop вставляет активную TOP-услугу со сроком и возвращает её ad/user id.
func seedTop(t *testing.T, ctx context.Context, r *repo.Repo, starts, ends time.Time, amount int64) (adID, userID string) {
	t.Helper()
	adID, userID = uuid.NewString(), uuid.NewString()
	ev, err := events.New(events.SubjectPromotionActivated, "payments", map[string]any{"ad_id": adID})
	require.NoError(t, err)
	require.NoError(t, r.ActivatePromotions(ctx, []domain.AdPromotion{{
		ID: uuid.NewString(), AdID: adID, UserID: userID, ServiceCode: "TOP",
		Status: domain.PromotionActive, AmountUZS: amount, StartsAt: starts, EndsAt: &ends,
	}}, ev))
	return adID, userID
}

// TestPromotionLifecycle_SuspendResumeRefund — полный цикл: приостановка при
// истечении объявления, возобновление со сдвигом срока, пропорциональный возврат.
func TestPromotionLifecycle_SuspendResumeRefund(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	start := time.Now().UTC().Add(-2 * 24 * time.Hour)
	end := start.Add(7 * 24 * time.Hour) // срок 7 дней, до конца ~5 дней
	adID, userID := seedTop(t, ctx, r, start, end, 70000)

	// Приостановка (истечение объявления): active → suspended, suspended_at выставлен.
	suspendNow := time.Now().UTC()
	n, err := r.SuspendPromotions(ctx, adID, suspendNow)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	susp, err := r.ListAdPromotions(ctx, adID, domain.PromotionSuspended)
	require.NoError(t, err)
	require.Len(t, susp, 1)
	require.NotNil(t, susp[0].SuspendedAt)
	origEnds := *susp[0].EndsAt

	// Повторная приостановка ничего не затрагивает (уже suspended).
	n, err = r.SuspendPromotions(ctx, adID, suspendNow)
	require.NoError(t, err)
	assert.Zero(t, n, "повторная приостановка идемпотентна")

	// Возобновление со сдвигом срока на длительность простоя (3ч).
	resumeNow := suspendNow.Add(3 * time.Hour)
	resumed, err := r.ResumePromotions(ctx, adID, resumeNow, nil)
	require.NoError(t, err)
	assert.True(t, resumed)

	active, err := r.ListAdPromotions(ctx, adID, domain.PromotionActive)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Nil(t, active[0].SuspendedAt, "suspended_at очищен")
	require.NotNil(t, active[0].EndsAt)
	assert.WithinDuration(t, origEnds.Add(3*time.Hour), *active[0].EndsAt, time.Minute,
		"срок сдвинут вперёд на простой")

	// Возврат неиспользованного срока при снятии объявления.
	amount := domain.RefundAmount(active[0], time.Now().UTC())
	require.Positive(t, amount)
	done, err := r.RefundPromotions(ctx, adID, userID, amount, "ad_blocked")
	require.NoError(t, err)
	assert.True(t, done)

	refunded, err := r.ListAdPromotions(ctx, adID, domain.PromotionRefunded)
	require.NoError(t, err)
	require.Len(t, refunded, 1)
	require.NotNil(t, refunded[0].RefundedAt)

	var bal int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT balance_uzs FROM wallets WHERE user_id=$1`, userID).Scan(&bal))
	assert.EqualValues(t, amount, bal, "возврат зачислен на кошелёк")

	var n2 int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectWalletRefunded).Scan(&n2))
	assert.Equal(t, 1, n2, "bozor.wallet.refunded опубликован")

	// Идемпотентность: повторный возврат не находит active/suspended → false, без двойного зачисления.
	done, err = r.RefundPromotions(ctx, adID, userID, amount, "ad_blocked")
	require.NoError(t, err)
	assert.False(t, done)
	require.NoError(t, pool.QueryRow(ctx, `SELECT balance_uzs FROM wallets WHERE user_id=$1`, userID).Scan(&bal))
	assert.EqualValues(t, amount, bal, "повторный возврат не удваивает баланс")
}

// TestRefundPromotions_ZeroAmountMarksWithoutCredit — истёкший срок: услуга
// переводится в refunded, но кошелёк не пополняется и события нет.
func TestRefundPromotions_ZeroAmountMarksWithoutCredit(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	start := time.Now().UTC().Add(-10 * 24 * time.Hour)
	end := start.Add(7 * 24 * time.Hour) // истёк 3 дня назад
	adID, userID := seedTop(t, ctx, r, start, end, 70000)

	done, err := r.RefundPromotions(ctx, adID, userID, 0, "ad_blocked")
	require.NoError(t, err)
	assert.True(t, done, "услуга завершена (refunded) даже без денежного возврата")

	refunded, err := r.ListAdPromotions(ctx, adID, domain.PromotionRefunded)
	require.NoError(t, err)
	require.Len(t, refunded, 1)

	var walletExists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wallets WHERE user_id=$1)`, userID).Scan(&walletExists))
	assert.False(t, walletExists, "нулевой возврат не создаёт кошелёк/проводку")

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectWalletRefunded).Scan(&n))
	assert.Zero(t, n, "нулевой возврат не публикует событие")
}
