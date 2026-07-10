package worker

import (
	"context"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/listing/internal/app"
	"bozor/services/listing/internal/domain"
)

// ExpiryStore — операции БД для истечения срока объявлений и продвижения.
type ExpiryStore interface {
	ListExpired(ctx context.Context, now time.Time, limit int) ([]domain.Ad, error)
	ExpireWithEvent(ctx context.Context, adID string, ev events.Envelope) (bool, error)
	ListExpiredPromos(ctx context.Context, now time.Time, limit int) ([]domain.Ad, error)
	ClearPromotionWithEvent(ctx context.Context, adID string, ev events.Envelope) (bool, error)
}

// Expirer периодически переводит активные объявления с истёкшим сроком
// (expires_at <= now) в статус expired, публикуя bozor.ad.expired. Тем же тиком
// снимает продвижение TOP с истёкшим промо (is_top AND promo_ends_at <= now),
// публикуя bozor.ad.updated, — так объявление уходит из топ-блока Search (8.6).
type Expirer struct {
	store    ExpiryStore
	interval time.Duration
	batch    int
	log      *slog.Logger
}

// NewExpirer создаёт воркер истечения срока объявлений.
func NewExpirer(store ExpiryStore, interval time.Duration, batch int, log *slog.Logger) *Expirer {
	return &Expirer{store: store, interval: interval, batch: batch, log: log}
}

// Run запускает периодическое истечение до отмены контекста.
func (e *Expirer) Run(ctx context.Context) error {
	t := time.NewTicker(e.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			e.Sweep(ctx)
		}
	}
}

// Sweep обрабатывает один пакет: истекает объявления по сроку и снимает
// продвижение TOP по promo_ends_at (используется тикером и тестами).
func (e *Expirer) Sweep(ctx context.Context) {
	e.sweepExpired(ctx)
	e.sweepPromos(ctx)
}

// sweepExpired переводит активные объявления с истёкшим сроком в expired.
func (e *Expirer) sweepExpired(ctx context.Context) {
	now := time.Now().UTC()
	ads, err := e.store.ListExpired(ctx, now, e.batch)
	if err != nil {
		e.log.ErrorContext(ctx, "истечение объявлений: выборка", slog.String("error", err.Error()))
		return
	}
	var expired int
	for _, ad := range ads {
		if e.expire(ctx, ad) {
			expired++
		}
	}
	if expired > 0 {
		e.log.InfoContext(ctx, "истёкшие объявления переведены в expired", slog.Int("count", expired))
	}
}

// sweepPromos снимает продвижение TOP с объявлений, чьё промо истекло.
func (e *Expirer) sweepPromos(ctx context.Context) {
	now := time.Now().UTC()
	ads, err := e.store.ListExpiredPromos(ctx, now, e.batch)
	if err != nil {
		e.log.ErrorContext(ctx, "истечение промо: выборка", slog.String("error", err.Error()))
		return
	}
	var cleared int
	for _, ad := range ads {
		if e.clearPromo(ctx, ad) {
			cleared++
		}
	}
	if cleared > 0 {
		e.log.InfoContext(ctx, "истёкшее продвижение TOP снято", slog.Int("count", cleared))
	}
}

// expire переводит одно объявление в expired и публикует событие; возвращает
// true, если статус действительно сменился (не был продлён/снят гонкой).
func (e *Expirer) expire(ctx context.Context, ad domain.Ad) bool {
	ad.Status = domain.StatusExpired
	ev, err := events.New(events.SubjectAdExpired, source, app.NewAdEvent(ad))
	if err != nil {
		e.log.ErrorContext(ctx, "истечение объявлений: сборка события",
			slog.String("ad_id", ad.ID), slog.String("error", err.Error()))
		return false
	}
	ok, err := e.store.ExpireWithEvent(ctx, ad.ID, ev)
	if err != nil {
		e.log.ErrorContext(ctx, "истечение объявлений: обновление",
			slog.String("ad_id", ad.ID), slog.String("error", err.Error()))
		return false
	}
	return ok
}

// clearPromo снимает продвижение TOP с одного объявления и публикует
// bozor.ad.updated; возвращает true, если флаг действительно снят (не продлён гонкой).
func (e *Expirer) clearPromo(ctx context.Context, ad domain.Ad) bool {
	ad.IsTop = false
	ad.PromotionRank = 0
	ad.PromoEndsAt = nil
	ev, err := events.New(events.SubjectAdUpdated, source, app.NewAdEvent(ad))
	if err != nil {
		e.log.ErrorContext(ctx, "истечение промо: сборка события",
			slog.String("ad_id", ad.ID), slog.String("error", err.Error()))
		return false
	}
	ok, err := e.store.ClearPromotionWithEvent(ctx, ad.ID, ev)
	if err != nil {
		e.log.ErrorContext(ctx, "истечение промо: обновление",
			slog.String("ad_id", ad.ID), slog.String("error", err.Error()))
		return false
	}
	return ok
}
