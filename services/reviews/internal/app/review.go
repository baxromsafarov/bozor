// Package app содержит use-cases Reviews-сервиса.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/reviews/internal/domain"
)

const source = "reviews"

// Пагинация ленты отзывов.
const (
	defaultLimit = 20
	maxLimit     = 100
)

// Store — персистентность отзывов (реализуется repo.Repo).
type Store interface {
	CreateWithEvent(ctx context.Context, rev domain.Review, ev events.Envelope) error
	ListByTarget(ctx context.Context, targetID string, limit, offset int) ([]domain.Review, error)
}

// Ads — чтение объявления из Listing (владелец = адресат отзыва).
type Ads interface {
	GetAd(ctx context.Context, id string) (domain.AdView, bool, error)
}

// Service — use-cases отзывов.
type Service struct {
	store Store
	ads   Ads
	log   *slog.Logger
}

// NewService создаёт сервис отзывов.
func NewService(store Store, ads Ads, log *slog.Logger) *Service {
	return &Service{store: store, ads: ads, log: log}
}

// CreateInput — входные данные создания отзыва.
type CreateInput struct {
	AuthorID string
	AdID     string
	Rating   int
	Body     string
}

// Create создаёт отзыв покупателя о продавце по объявлению: валидирует оценку и
// текст, определяет адресата (владельца объявления из Listing), запрещает отзыв о
// собственном объявлении и повторный отзыв по тому же объявлению (анти-абьюз),
// публикует bozor.review.created. Адресат — источник истины из Listing, клиент его
// не задаёт (IDOR-защита).
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Review, error) {
	if err := domain.ValidateRating(in.Rating); err != nil {
		return domain.Review{}, err
	}
	body, err := domain.NormalizeBody(in.Body)
	if err != nil {
		return domain.Review{}, err
	}

	ad, found, err := s.ads.GetAd(ctx, in.AdID)
	if err != nil {
		return domain.Review{}, err
	}
	if !found {
		return domain.Review{}, domain.ErrAdNotFound
	}
	if ad.UserID == in.AuthorID {
		return domain.Review{}, domain.ErrSelfReview
	}

	now := time.Now().UTC()
	id, err := uuid.NewV7()
	if err != nil {
		return domain.Review{}, fmt.Errorf("app: генерация id: %w", err)
	}
	rev := domain.Review{
		ID: id.String(), AdID: in.AdID, AuthorID: in.AuthorID, TargetID: ad.UserID,
		Rating: in.Rating, Body: body, Status: domain.StatusActive, CreatedAt: now, UpdatedAt: now,
	}

	ev, err := events.New(events.SubjectReviewCreated, source, NewReviewEvent(rev))
	if err != nil {
		return domain.Review{}, fmt.Errorf("app: сборка события: %w", err)
	}
	if err := s.store.CreateWithEvent(ctx, rev, ev); err != nil {
		return domain.Review{}, err // включая ErrDuplicateReview
	}
	return rev, nil
}

// ListByUser возвращает активные отзывы о пользователе (свежие сверху) с пагинацией.
func (s *Service) ListByUser(ctx context.Context, targetID string, limit, offset int) ([]domain.Review, error) {
	return s.store.ListByTarget(ctx, targetID, clampLimit(limit), clampOffset(offset))
}

// ReviewEvent — полезная нагрузка bozor.review.created (агрегатор рейтинга 9.2
// пересчитывает avg_rating/count адресата; Notification уведомляет продавца).
type ReviewEvent struct {
	ReviewID string `json:"review_id"`
	AdID     string `json:"ad_id"`
	AuthorID string `json:"author_id"`
	TargetID string `json:"target_id"`
	Rating   int    `json:"rating"`
}

// NewReviewEvent собирает нагрузку события создания отзыва.
func NewReviewEvent(rev domain.Review) ReviewEvent {
	return ReviewEvent{
		ReviewID: rev.ID, AdID: rev.AdID, AuthorID: rev.AuthorID,
		TargetID: rev.TargetID, Rating: rev.Rating,
	}
}

func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultLimit
	case limit > maxLimit:
		return maxLimit
	default:
		return limit
	}
}

func clampOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}
