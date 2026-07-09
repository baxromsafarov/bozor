package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/notification/internal/domain"
)

func TestPrefGroup(t *testing.T) {
	cases := []struct {
		subject string
		group   string
		known   bool
	}{
		{events.SubjectAdApproved, domain.GroupAdStatus, true},
		{events.SubjectAdRejected, domain.GroupAdStatus, true},
		{events.SubjectAdExpired, domain.GroupAdStatus, true},
		{events.SubjectChatMessageSent, domain.GroupChatMessage, true},
		{events.SubjectSavedSearchMatched, domain.GroupSavedSearch, true},
		{events.SubjectReviewCreated, domain.GroupReview, true},
		{events.SubjectPromotionActivated, domain.GroupPromotion, true},
		{events.SubjectPaymentSucceeded, domain.GroupPromotion, true},
		{events.SubjectPaymentFailed, domain.GroupPromotion, true},
		{events.SubjectWalletRefunded, domain.GroupPromotion, true},
		{events.SubjectUserBanned, domain.GroupNone, true}, // бан — всегда, минуя настройки
		{"bozor.unknown.subject", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.subject, func(t *testing.T) {
			g, known := domain.PrefGroup(tc.subject)
			assert.Equal(t, tc.known, known)
			assert.Equal(t, tc.group, g)
		})
	}
}

// TestSubjectsCoverAllTemplates — каждый слушаемый subject имеет шаблон на обоих
// языках, и наоборот: список Subjects согласован с маппингом групп.
func TestSubjectsCoverAllTemplates(t *testing.T) {
	for _, s := range domain.Subjects() {
		_, known := domain.PrefGroup(s)
		require.True(t, known, "subject %s должен иметь группу", s)

		ru, ok := domain.Render(s, "ru", samplePayload())
		require.True(t, ok, "нет ru-шаблона для %s", s)
		require.NotEmpty(t, ru)

		uz, ok := domain.Render(s, "uz", samplePayload())
		require.True(t, ok, "нет uz-шаблона для %s", s)
		require.NotEmpty(t, uz)

		assert.NotEqual(t, ru, uz, "ru и uz шаблоны для %s не должны совпадать", s)
	}
}

func TestRender_UnknownSubject(t *testing.T) {
	_, ok := domain.Render("bozor.nope", "ru", samplePayload())
	assert.False(t, ok)
}

func TestRender_Localization(t *testing.T) {
	p := domain.EventPayload{Title: "Toyota Camry"}

	ru, ok := domain.Render(events.SubjectAdApproved, "ru", p)
	require.True(t, ok)
	assert.Contains(t, ru, "одобрено")
	assert.Contains(t, ru, "Toyota Camry")

	uz, ok := domain.Render(events.SubjectAdApproved, "uz", p)
	require.True(t, ok)
	assert.Contains(t, uz, "tasdiq")
	assert.Contains(t, uz, "Toyota Camry")

	// Неизвестный язык откатывается к ru.
	def, ok := domain.Render(events.SubjectAdApproved, "en", p)
	require.True(t, ok)
	assert.Equal(t, ru, def)
}

func TestRender_RejectedWithReason(t *testing.T) {
	withReason, _ := domain.Render(events.SubjectAdRejected, "ru",
		domain.EventPayload{Title: "iPhone", Reason: "стоп-слово"})
	assert.Contains(t, withReason, "стоп-слово")

	noReason, _ := domain.Render(events.SubjectAdRejected, "ru",
		domain.EventPayload{Title: "iPhone"})
	assert.NotContains(t, noReason, "Причина")
}

func TestRender_PaymentMoney(t *testing.T) {
	withMoney, _ := domain.Render(events.SubjectPaymentSucceeded, "ru",
		domain.EventPayload{Amount: 50000, Currency: "UZS"})
	assert.Contains(t, withMoney, "50000 UZS")

	// Нулевая сумма не ломает шаблон.
	zero, _ := domain.Render(events.SubjectPaymentSucceeded, "ru", domain.EventPayload{})
	assert.NotEmpty(t, zero)
	assert.False(t, strings.Contains(zero, "0 "))
}

func TestNormalizeLang(t *testing.T) {
	assert.Equal(t, "uz", domain.NormalizeLang("uz"))
	assert.Equal(t, "uz", domain.NormalizeLang("UZ-Latn"))
	assert.Equal(t, "ru", domain.NormalizeLang("ru"))
	assert.Equal(t, "ru", domain.NormalizeLang(""))
	assert.Equal(t, "ru", domain.NormalizeLang("en"))
}

func samplePayload() domain.EventPayload {
	return domain.EventPayload{
		UserID: "u1", AdID: "a1", Title: "Тестовое объявление", Name: "Мой поиск",
		Reason: "причина", Amount: 1000, Currency: "UZS", SenderName: "Азиз", Until: "2026-08-01",
	}
}
