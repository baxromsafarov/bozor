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

// TestActivatePromotions_PersistsAndEmits — активация вставляет услуги и кладёт
// bozor.promotion.activated в outbox; список отдаёт их с расписанием.
func TestActivatePromotions_PersistsAndEmits(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	adID, userID := uuid.NewString(), uuid.NewString()
	bundle := "FAST_SALE"
	ends := time.Now().UTC().Add(7 * 24 * time.Hour)
	promos := []domain.AdPromotion{
		{ID: uuid.NewString(), AdID: adID, UserID: userID, ServiceCode: "TOP", BundleCode: &bundle,
			Status: domain.PromotionActive, AmountUZS: 45000, StartsAt: time.Now().UTC(), EndsAt: &ends},
		{ID: uuid.NewString(), AdID: adID, UserID: userID, ServiceCode: "BUMP", BundleCode: &bundle,
			Status: domain.PromotionActive, Schedule: []int{2, 4, 6}, StartsAt: time.Now().UTC()},
	}
	ev, err := events.New(events.SubjectPromotionActivated, "payments", map[string]any{"ad_id": adID, "is_top": true})
	require.NoError(t, err)
	require.NoError(t, r.ActivatePromotions(ctx, promos, ev))

	var outboxN int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectPromotionActivated).Scan(&outboxN))
	assert.Equal(t, 1, outboxN)

	got, err := r.ListAdPromotions(ctx, adID, domain.PromotionActive)
	require.NoError(t, err)
	require.Len(t, got, 2)

	var bump domain.AdPromotion
	for _, p := range got {
		if p.ServiceCode == "BUMP" {
			bump = p
		}
	}
	assert.Equal(t, []int{2, 4, 6}, bump.Schedule, "расписание сохранено в jsonb")
}

// TestRefund_CreditsAndEmits — компенсирующий возврат зачисляет средства и кладёт
// bozor.wallet.refunded в outbox.
func TestRefund_CreditsAndEmits(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	userID := uuid.NewString()
	ref := "ad-1"
	w, err := r.Refund(ctx, userID, 30000, &ref, "activation_failed")
	require.NoError(t, err)
	assert.EqualValues(t, 30000, w.BalanceUZS)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectWalletRefunded).Scan(&n))
	assert.Equal(t, 1, n)

	// Проводка возврата — kind=refund.
	var kind string
	require.NoError(t, pool.QueryRow(ctx, `SELECT kind FROM wallet_transactions WHERE wallet_user_id=$1`, userID).Scan(&kind))
	assert.Equal(t, domain.KindRefund, kind)
}
