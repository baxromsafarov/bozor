package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRefundAmount(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := base
	end := base.Add(10 * 24 * time.Hour) // срок 10 дней

	// Активная, прошло 4 из 10 дней → возврат за 6 неиспользованных (60%).
	active := AdPromotion{Status: PromotionActive, AmountUZS: 10000, StartsAt: start, EndsAt: &end}
	assert.EqualValues(t, 6000, RefundAmount(active, base.Add(4*24*time.Hour)))

	// Полностью истёкшая → 0.
	assert.EqualValues(t, 0, RefundAmount(active, end.Add(time.Hour)))

	// Без срока (BUMP) → 0.
	assert.EqualValues(t, 0, RefundAmount(AdPromotion{Status: PromotionActive, AmountUZS: 5000, StartsAt: start}, base))

	// Нулевая стоимость (не первая услуга набора) → 0.
	assert.EqualValues(t, 0, RefundAmount(AdPromotion{Status: PromotionActive, AmountUZS: 0, StartsAt: start, EndsAt: &end}, base))

	// Приостановленная: остаток считается от момента заморозки, не от now.
	susp := base.Add(3 * 24 * time.Hour) // заморожена на 3-й день → неиспользовано 7 дней
	suspended := AdPromotion{Status: PromotionSuspended, AmountUZS: 10000, StartsAt: start, EndsAt: &end, SuspendedAt: &susp}
	assert.EqualValues(t, 7000, RefundAmount(suspended, base.Add(9*24*time.Hour)),
		"простой в suspended не съедает срок")
}
