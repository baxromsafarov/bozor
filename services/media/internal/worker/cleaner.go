package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/media/internal/domain"
)

// OrphanStore — операции БД для очистки сирот (реализуется repo.Repo).
type OrphanStore interface {
	ListOrphans(ctx context.Context, olderThan time.Time, limit int) ([]domain.Media, error)
	DeleteWithEvent(ctx context.Context, id string, ev events.Envelope) error
}

// Cleaner периодически удаляет «сирот» — медиа без привязки к объявлению
// (ad_id IS NULL), пролежавшие дольше TTL. Удаляет объекты (оригинал + превью)
// из хранилища и запись из БД, публикуя bozor.media.deleted.
type Cleaner struct {
	store    OrphanStore
	blob     Blob
	ttl      time.Duration
	interval time.Duration
	batch    int
	log      *slog.Logger
}

// NewCleaner создаёт очиститель сирот.
func NewCleaner(store OrphanStore, blob Blob, ttl, interval time.Duration, batch int, log *slog.Logger) *Cleaner {
	return &Cleaner{store: store, blob: blob, ttl: ttl, interval: interval, batch: batch, log: log}
}

// Run запускает периодическую очистку до отмены контекста.
func (c *Cleaner) Run(ctx context.Context) error {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			c.Sweep(ctx)
		}
	}
}

// Sweep удаляет один пакет сирот (используется тикером и тестами).
func (c *Cleaner) Sweep(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-c.ttl)
	orphans, err := c.store.ListOrphans(ctx, cutoff, c.batch)
	if err != nil {
		c.log.ErrorContext(ctx, "очистка сирот: выборка", slog.String("error", err.Error()))
		return
	}
	var removed int
	for _, m := range orphans {
		if c.remove(ctx, m) {
			removed++
		}
	}
	if removed > 0 {
		c.log.InfoContext(ctx, "очищены медиа-сироты", slog.Int("count", removed))
	}
}

// remove удаляет объекты и запись одной сироты; возвращает true при успехе.
func (c *Cleaner) remove(ctx context.Context, m domain.Media) bool {
	// Блобы удаляем best-effort (RemoveObject идемпотентен): оригинал + превью.
	c.removeBlob(ctx, m.ObjectKey)
	for _, p := range m.Previews {
		c.removeBlob(ctx, p.ObjectKey)
	}

	ev, err := events.New(events.SubjectMediaDeleted, source, deletedPayload(m))
	if err != nil {
		c.log.ErrorContext(ctx, "очистка сирот: сборка события", slog.String("error", err.Error()))
		return false
	}
	if err := c.store.DeleteWithEvent(ctx, m.ID, ev); err != nil {
		if !errors.Is(err, domain.ErrMediaNotFound) {
			c.log.ErrorContext(ctx, "очистка сирот: удаление записи",
				slog.String("media_id", m.ID), slog.String("error", err.Error()))
		}
		return false
	}
	return true
}

// removeBlob удаляет объект из хранилища, логируя ошибку без прерывания.
func (c *Cleaner) removeBlob(ctx context.Context, key string) {
	if err := c.blob.Remove(ctx, key); err != nil {
		c.log.WarnContext(ctx, "очистка сирот: удаление объекта",
			slog.String("object_key", key), slog.String("error", err.Error()))
	}
}
