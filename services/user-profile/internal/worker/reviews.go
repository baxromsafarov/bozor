package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/user-profile/internal/domain"
)

// ReviewsConsumer — имя durable-консьюмера и ключ inbox-идемпотентности
// агрегатора рейтинга.
const ReviewsConsumer = "user-profile-reviews"

// ReviewsStore — операции БД, нужные агрегатору рейтинга.
type ReviewsStore interface {
	IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error)
	UpsertRatingWithInbox(ctx context.Context, def domain.Profile, rt domain.Rating, consumer, eventID string) error
}

// Ratings — источник актуального агрегата рейтинга (реализуется reviewsclient.Client).
type Ratings interface {
	GetRating(ctx context.Context, userID string) (domain.Rating, error)
}

// Reviews потребляет bozor.review.created и обновляет кеш агрегированного
// рейтинга продавца. Агрегат не берётся из события (в нём лишь одна оценка), а
// перечитывается из Reviews по адресату (fetch-current-state, ADR-024): так
// снятые модератором отзывы автоматически выпадают из рейтинга, а повторная
// доставка безопасна (пересчёт к текущему значению).
type Reviews struct {
	store   ReviewsStore
	ratings Ratings
	log     *slog.Logger
}

// NewReviews создаёт агрегатор рейтинга.
func NewReviews(store ReviewsStore, ratings Ratings, log *slog.Logger) *Reviews {
	return &Reviews{store: store, ratings: ratings, log: log}
}

// reviewCreated — интересующая часть события bozor.review.created: адресат отзыва
// (продавец), чей рейтинг пересчитывается.
type reviewCreated struct {
	TargetID string `json:"target_id"`
}

// Handle обрабатывает одно событие создания отзыва. Ошибка → повтор/DLQ (natsx),
// nil → подтверждение. Идемпотентно: inbox + пересчёт агрегата к текущему значению.
func (c *Reviews) Handle(ctx context.Context, env events.Envelope) error {
	var pl reviewCreated
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор bozor.review.created: %w", err)
	}
	if pl.TargetID == "" {
		return errors.New("worker: пустой target_id в bozor.review.created")
	}

	processed, err := c.store.IsEventProcessed(ctx, ReviewsConsumer, env.ID)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}

	rt, err := c.ratings.GetRating(ctx, pl.TargetID)
	if err != nil {
		return err // недоступность Reviews — повторяемо
	}

	def := domain.NewDefaultProfile(pl.TargetID, "", time.Now().UTC())
	c.log.InfoContext(ctx, "обновление рейтинга по отзыву",
		slog.String("user_id", pl.TargetID),
		slog.Int("reviews_count", rt.ReviewsCount))
	return c.store.UpsertRatingWithInbox(ctx, def, rt, ReviewsConsumer, env.ID)
}
