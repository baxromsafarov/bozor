package worker

import (
	"context"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/listing/internal/app"
	"bozor/services/listing/internal/domain"
)

// ExpiryStore — операции БД для истечения срока объявлений.
type ExpiryStore interface {
	ListExpired(ctx context.Context, now time.Time, limit int) ([]domain.Ad, error)
	ExpireWithEvent(ctx context.Context, adID string, ev events.Envelope) (bool, error)
}

// Expirer периодически переводит активные объявления с истёкшим сроком
// (expires_at <= now) в статус expired, публикуя bozor.ad.expired.
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

// Sweep истекает один пакет объявлений (используется тикером и тестами).
func (e *Expirer) Sweep(ctx context.Context) {
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
