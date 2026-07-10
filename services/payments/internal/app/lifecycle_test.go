package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/payments/internal/domain"
)

type fakeLifecycleRepo struct {
	byStatus     map[string][]domain.AdPromotion
	suspendN     int
	suspendErr   error
	resumeOK     bool
	resumeEv     *events.Envelope
	resumeErr    error
	refundOK     bool
	refundUser   string
	refundAmount int64
	refundCalled bool
	refundErr    error
}

func (f *fakeLifecycleRepo) ListAdPromotions(_ context.Context, _, status string) ([]domain.AdPromotion, error) {
	return f.byStatus[status], nil
}

func (f *fakeLifecycleRepo) SuspendPromotions(_ context.Context, _ string, _ time.Time) (int, error) {
	return f.suspendN, f.suspendErr
}

func (f *fakeLifecycleRepo) ResumePromotions(_ context.Context, _ string, _ time.Time, ev *events.Envelope) (bool, error) {
	f.resumeEv = ev
	return f.resumeOK, f.resumeErr
}

func (f *fakeLifecycleRepo) RefundPromotions(_ context.Context, _, userID string, amount int64, _ string) (bool, error) {
	f.refundCalled = true
	f.refundUser = userID
	f.refundAmount = amount
	return f.refundOK, f.refundErr
}

func TestLifecycleService_Suspend_Delegates(t *testing.T) {
	repo := &fakeLifecycleRepo{suspendN: 3}
	n, err := NewLifecycleService(repo, discardLog()).Suspend(context.Background(), "ad-1")
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}

func TestLifecycleService_Refund_SumsUnusedDays(t *testing.T) {
	base := time.Now().UTC()
	end := base.Add(10 * 24 * time.Hour)
	repo := &fakeLifecycleRepo{
		byStatus: map[string][]domain.AdPromotion{"": {
			{UserID: "u1", ServiceCode: "TOP", Status: domain.PromotionActive, AmountUZS: 10000, StartsAt: base, EndsAt: &end},
			{UserID: "u1", ServiceCode: "BUMP", Status: domain.PromotionActive, AmountUZS: 0, StartsAt: base},
			{UserID: "u1", ServiceCode: "TOP", Status: domain.PromotionRefunded, AmountUZS: 5000, StartsAt: base, EndsAt: &end},
		}},
		refundOK: true,
	}
	done, err := NewLifecycleService(repo, discardLog()).Refund(context.Background(), "ad-1", "ad_blocked")
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, "u1", repo.refundUser)
	// Почти весь срок TOP (10000) не использован; BUMP/уже-refunded не учитываются.
	assert.Greater(t, repo.refundAmount, int64(9000))
	assert.LessOrEqual(t, repo.refundAmount, int64(10000))
}

func TestLifecycleService_Refund_NoActivePromosNoop(t *testing.T) {
	end := time.Now().UTC().Add(time.Hour)
	repo := &fakeLifecycleRepo{byStatus: map[string][]domain.AdPromotion{"": {
		{UserID: "u1", ServiceCode: "TOP", Status: domain.PromotionRefunded, AmountUZS: 10000, EndsAt: &end},
	}}}
	done, err := NewLifecycleService(repo, discardLog()).Refund(context.Background(), "ad-1", "ad_deleted")
	require.NoError(t, err)
	assert.False(t, done)
	assert.False(t, repo.refundCalled, "нет активных/приостановленных — возврат не вызывается")
}

func TestLifecycleService_Resume_BuildsTopEvent(t *testing.T) {
	base := time.Now().UTC()
	end := base.Add(5 * 24 * time.Hour)
	susp := base.Add(-2 * 24 * time.Hour) // приостановлена 2 дня назад
	repo := &fakeLifecycleRepo{
		byStatus: map[string][]domain.AdPromotion{domain.PromotionSuspended: {
			{UserID: "u1", ServiceCode: "TOP", Status: domain.PromotionSuspended,
				AmountUZS: 10000, StartsAt: base.Add(-3 * 24 * time.Hour), EndsAt: &end, SuspendedAt: &susp},
		}},
		resumeOK: true,
	}
	resumed, err := NewLifecycleService(repo, discardLog()).Resume(context.Background(), "ad-1", "Camry 2019")
	require.NoError(t, err)
	assert.True(t, resumed)
	require.NotNil(t, repo.resumeEv, "TOP-услуга → событие продвижения")

	var pl promotionActivatedPayload
	require.NoError(t, repo.resumeEv.Decode(&pl))
	assert.True(t, pl.IsTop)
	assert.Equal(t, "Camry 2019", pl.Title)
	require.NotNil(t, pl.EndsAt)
	assert.True(t, pl.EndsAt.After(end), "срок сдвинут вперёд на длительность простоя")
}

func TestLifecycleService_Resume_NoTopNoEvent(t *testing.T) {
	end := time.Now().UTC().Add(5 * 24 * time.Hour)
	susp := time.Now().UTC()
	repo := &fakeLifecycleRepo{
		byStatus: map[string][]domain.AdPromotion{domain.PromotionSuspended: {
			{UserID: "u1", ServiceCode: "VIP", Status: domain.PromotionSuspended,
				AmountUZS: 5000, StartsAt: time.Now().UTC(), EndsAt: &end, SuspendedAt: &susp},
		}},
		resumeOK: true,
	}
	resumed, err := NewLifecycleService(repo, discardLog()).Resume(context.Background(), "ad-1", "X")
	require.NoError(t, err)
	assert.True(t, resumed)
	assert.Nil(t, repo.resumeEv, "без TOP среди приостановленных — событие не строится")
}

func TestLifecycleService_Resume_NoSuspendedNoop(t *testing.T) {
	repo := &fakeLifecycleRepo{byStatus: map[string][]domain.AdPromotion{domain.PromotionSuspended: nil}}
	resumed, err := NewLifecycleService(repo, discardLog()).Resume(context.Background(), "ad-1", "X")
	require.NoError(t, err)
	assert.False(t, resumed)
}
