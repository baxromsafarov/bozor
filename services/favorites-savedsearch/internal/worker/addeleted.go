// Package worker содержит фоновые процессы Favorites/SavedSearch-сервиса:
// очистку избранного при удалении объявления (bozor.ad.deleted).
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"bozor/pkg/shared/events"
)

// AdDeletedConsumer — имя durable-консьюмера очистки избранного.
const AdDeletedConsumer = "favorites-ad-deleted"

// FavoritesStore — операции, нужные обработчику удаления объявления.
type FavoritesStore interface {
	RemoveByAd(ctx context.Context, adID string) (int64, error)
}

// AdDeleted убирает удалённое объявление из избранного всех пользователей.
type AdDeleted struct {
	store FavoritesStore
	log   *slog.Logger
}

// NewAdDeleted создаёт обработчик bozor.ad.deleted.
func NewAdDeleted(store FavoritesStore, log *slog.Logger) *AdDeleted {
	return &AdDeleted{store: store, log: log}
}

// adDeletedPayload — интересующая часть события bozor.ad.deleted.
type adDeletedPayload struct {
	AdID string `json:"ad_id"`
}

// Handle обрабатывает удаление объявления: удаляет его из избранного (идемпотентно
// — DELETE по ad_id, повторная доставка безопасна, inbox не нужен).
func (a *AdDeleted) Handle(ctx context.Context, env events.Envelope) error {
	var pl adDeletedPayload
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор bozor.ad.deleted: %w", err)
	}
	if pl.AdID == "" {
		return errors.New("worker: пустой ad_id в bozor.ad.deleted")
	}
	n, err := a.store.RemoveByAd(ctx, pl.AdID)
	if err != nil {
		return err
	}
	if n > 0 {
		a.log.InfoContext(ctx, "объявление убрано из избранного",
			slog.String("ad_id", pl.AdID), slog.Int64("removed", n))
	}
	return nil
}
