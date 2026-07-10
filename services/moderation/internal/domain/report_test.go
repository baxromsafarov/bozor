package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"bozor/services/moderation/internal/domain"
)

func TestValidateReportInput(t *testing.T) {
	assert.NoError(t, domain.ValidateReportInput(domain.TargetAd, "ad1", "спам"))
	assert.ErrorIs(t, domain.ValidateReportInput("planet", "x", "спам"), domain.ErrInvalidTarget)
	assert.ErrorIs(t, domain.ValidateReportInput(domain.TargetAd, "  ", "спам"), domain.ErrInvalidTarget)
	assert.ErrorIs(t, domain.ValidateReportInput(domain.TargetAd, "ad1", ""), domain.ErrReasonRequired)
}

func TestValidateAction(t *testing.T) {
	assert.NoError(t, domain.ValidateAction(domain.ActionDismiss, domain.TargetUser))
	assert.NoError(t, domain.ValidateAction(domain.ActionTakedown, domain.TargetAd))
	assert.NoError(t, domain.ValidateAction(domain.ActionTakedown, domain.TargetMessage), "снятие применимо и к сообщению")
	assert.NoError(t, domain.ValidateAction(domain.ActionTakedown, domain.TargetReview), "снятие применимо и к отзыву")
	assert.ErrorIs(t, domain.ValidateAction("nuke", domain.TargetAd), domain.ErrInvalidAction)
	// takedown к пользователю — недопустим (для пользователя есть бан).
	assert.ErrorIs(t, domain.ValidateAction(domain.ActionTakedown, domain.TargetUser), domain.ErrTakedownTarget)
}

func TestResolvedStatus(t *testing.T) {
	assert.Equal(t, domain.ReportDismissed, domain.ResolvedStatus(domain.ActionDismiss))
	assert.Equal(t, domain.ReportResolved, domain.ResolvedStatus(domain.ActionTakedown))
	assert.Equal(t, domain.ReportResolved, domain.ResolvedStatus(domain.ActionWarn))
}

func TestValidateBanInput(t *testing.T) {
	assert.NoError(t, domain.ValidateBanInput(domain.BanPermanent, 0))
	assert.NoError(t, domain.ValidateBanInput(domain.BanShadow, 0))
	assert.NoError(t, domain.ValidateBanInput(domain.BanTemporary, time.Hour))
	assert.ErrorIs(t, domain.ValidateBanInput("forever", 0), domain.ErrInvalidBanType)
	assert.ErrorIs(t, domain.ValidateBanInput(domain.BanTemporary, 0), domain.ErrBanDurationRequired)
}

func TestBanExpiry(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	exp := domain.BanExpiry(domain.BanTemporary, now, time.Hour)
	require := assert.New(t)
	require.NotNil(exp)
	require.Equal(now.Add(time.Hour), *exp)

	assert.Nil(t, domain.BanExpiry(domain.BanPermanent, now, time.Hour))
	assert.Nil(t, domain.BanExpiry(domain.BanShadow, now, time.Hour))
}
