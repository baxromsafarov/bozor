// Package worker содержит фоновые процессы Payments/Promotions-сервиса.
// Bumper — воркер авто-поднятий (Stage 8.5): по расписанию услуг BUMP поднимает
// объявления в ленте, дёргая внутренний эндпоинт Listing (владельца bumped_at).
package worker

import (
	"context"
	"log/slog"
	"time"

	"bozor/services/payments/internal/domain"
)

// BumpStore — доступ к созревшим поднятиям и их идемпотентной отметке.
type BumpStore interface {
	DueBumps(ctx context.Context, limit int) ([]domain.DueBump, error)
	ClaimBump(ctx context.Context, promotionID string, dayOffset int) (bool, error)
	ReleaseBump(ctx context.Context, promotionID string, dayOffset int) error
}

// BumpTarget поднимает объявление в ленте (реализуется listingclient.Client).
// Возвращает bumped=false, если объявления нет/оно не активно (день исполнен впустую).
type BumpTarget interface {
	Bump(ctx context.Context, adID string) (bool, error)
}

// Bumper периодически исполняет созревшие дни расписаний авто-поднятия: для
// каждого атомарно застолбляет день (bump_runs) и просит Listing поднять
// объявление. Listing выставляет bumped_at и публикует bozor.ad.bumped, по
// которому Search переиндексирует карточку (fetch-current-state, ADR-024).
type Bumper struct {
	store    BumpStore
	listing  BumpTarget
	interval time.Duration
	batch    int
	log      *slog.Logger
}

// NewBumper создаёт воркер авто-поднятий.
func NewBumper(store BumpStore, listing BumpTarget, interval time.Duration, batch int, log *slog.Logger) *Bumper {
	return &Bumper{store: store, listing: listing, interval: interval, batch: batch, log: log}
}

// Run запускает периодическое исполнение поднятий до отмены контекста.
func (b *Bumper) Run(ctx context.Context) error {
	t := time.NewTicker(b.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			b.Sweep(ctx)
		}
	}
}

// Sweep исполняет один пакет созревших поднятий (используется тикером и тестами).
func (b *Bumper) Sweep(ctx context.Context) {
	due, err := b.store.DueBumps(ctx, b.batch)
	if err != nil {
		b.log.ErrorContext(ctx, "авто-поднятие: выборка созревших", slog.String("error", err.Error()))
		return
	}
	var bumped int
	for _, d := range due {
		if b.run(ctx, d) {
			bumped++
		}
	}
	if bumped > 0 {
		b.log.InfoContext(ctx, "объявления подняты авто-поднятием", slog.Int("count", bumped))
	}
}

// run исполняет одно созревшее поднятие: столбит день, затем поднимает объявление
// в Listing. При ошибке Listing день освобождается для повторной попытки на
// следующем тике. Возвращает true, если объявление действительно поднято.
func (b *Bumper) run(ctx context.Context, d domain.DueBump) bool {
	claimed, err := b.store.ClaimBump(ctx, d.PromotionID, d.DayOffset)
	if err != nil {
		b.log.ErrorContext(ctx, "авто-поднятие: резервирование дня",
			slog.String("promotion_id", d.PromotionID), slog.Int("day", d.DayOffset),
			slog.String("error", err.Error()))
		return false
	}
	if !claimed {
		return false // день уже застолблён (гонка/повтор) — пропуск
	}

	bumped, err := b.listing.Bump(ctx, d.AdID)
	if err != nil {
		// Транзиентная ошибка Listing — освобождаем день, повторим на след. тике.
		if rerr := b.store.ReleaseBump(ctx, d.PromotionID, d.DayOffset); rerr != nil {
			b.log.ErrorContext(ctx, "авто-поднятие: освобождение дня",
				slog.String("promotion_id", d.PromotionID), slog.Int("day", d.DayOffset),
				slog.String("error", rerr.Error()))
		}
		b.log.ErrorContext(ctx, "авто-поднятие: поднятие в Listing",
			slog.String("ad_id", d.AdID), slog.String("error", err.Error()))
		return false
	}
	if !bumped {
		// Объявления нет/не активно — день считается исполненным (резерв оставляем),
		// повторно дёргать Listing по нему смысла нет.
		b.log.InfoContext(ctx, "авто-поднятие: объявление не активно, пропуск",
			slog.String("ad_id", d.AdID), slog.String("promotion_id", d.PromotionID))
		return false
	}
	return true
}
