// Package worker содержит фоновые консьюмеры Reviews-сервиса: снятие отзыва
// модератором по жалобе (bozor.review.blocked).
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"bozor/pkg/shared/events"
)

// BlockConsumer — имя durable-консьюмера снятия отзывов и ключ inbox.
const BlockConsumer = "reviews-block"

// Store — операции снятия отзыва и inbox-идемпотентности (реализуется repo.Repo).
type Store interface {
	BlockReview(ctx context.Context, reviewID string) error
	IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, consumer, eventID string) error
}

// Moderator скрывает отзыв, снятый модератором по жалобе. Идемпотентно через inbox.
type Moderator struct {
	store Store
	log   *slog.Logger
}

// New создаёт консьюмер снятия отзывов.
func New(store Store, log *slog.Logger) *Moderator {
	return &Moderator{store: store, log: log}
}

// reviewBlocked — нужная часть bozor.review.blocked.
type reviewBlocked struct {
	ReviewID string `json:"review_id"`
	Reason   string `json:"reason"`
}

// HandleBlock снимает отзыв по bozor.review.blocked (status → blocked). Идемпотентно.
func (m *Moderator) HandleBlock(ctx context.Context, env events.Envelope) error {
	var pl reviewBlocked
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор bozor.review.blocked: %w", err)
	}
	if pl.ReviewID == "" {
		return errors.New("worker: пустой review_id в bozor.review.blocked")
	}

	processed, err := m.store.IsEventProcessed(ctx, BlockConsumer, env.ID)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}
	if err := m.store.BlockReview(ctx, pl.ReviewID); err != nil {
		return err
	}
	m.log.InfoContext(ctx, "отзыв снят модератором", slog.String("review_id", pl.ReviewID))
	return m.store.MarkEventProcessed(ctx, BlockConsumer, env.ID)
}
