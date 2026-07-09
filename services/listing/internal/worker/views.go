package worker

import (
	"context"
	"log/slog"
	"time"
)

// ViewFlushStore прибавляет накопленные просмотры к объявлениям (реализуется repo.Repo).
type ViewFlushStore interface {
	AddViews(ctx context.Context, counts map[string]int64) error
}

// ViewBuffer — буфер просмотров в Redis (реализуется views.Counter).
type ViewBuffer interface {
	Drain(ctx context.Context) (map[string]int64, error)
	Restore(ctx context.Context, counts map[string]int64) error
}

// ViewFlusher периодически сливает буфер просмотров из Redis в PostgreSQL пачкой,
// устраняя write-hotspot от потока просмотров популярных объявлений (Stage 3.5).
type ViewFlusher struct {
	store    ViewFlushStore
	buffer   ViewBuffer
	interval time.Duration
	log      *slog.Logger
}

// NewViewFlusher создаёт воркер флеша просмотров.
func NewViewFlusher(store ViewFlushStore, buffer ViewBuffer, interval time.Duration, log *slog.Logger) *ViewFlusher {
	return &ViewFlusher{store: store, buffer: buffer, interval: interval, log: log}
}

// Run запускает периодический флеш до отмены контекста.
func (f *ViewFlusher) Run(ctx context.Context) error {
	t := time.NewTicker(f.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			f.Flush(ctx)
		}
	}
}

// Flush снимает буфер и прибавляет просмотры к объявлениям одной пачкой. При
// сбое записи в БД счётчики возвращаются в буфер (повтор на следующем тике).
func (f *ViewFlusher) Flush(ctx context.Context) {
	counts, err := f.buffer.Drain(ctx)
	if err != nil {
		f.log.ErrorContext(ctx, "флеш просмотров: снятие буфера", slog.String("error", err.Error()))
		return
	}
	if len(counts) == 0 {
		return
	}
	if err := f.store.AddViews(ctx, counts); err != nil {
		f.log.ErrorContext(ctx, "флеш просмотров: запись в БД", slog.String("error", err.Error()))
		if rerr := f.buffer.Restore(ctx, counts); rerr != nil {
			f.log.ErrorContext(ctx, "флеш просмотров: возврат буфера", slog.String("error", rerr.Error()))
		}
		return
	}
	f.log.InfoContext(ctx, "просмотры слиты в БД", slog.Int("ads", len(counts)))
}
