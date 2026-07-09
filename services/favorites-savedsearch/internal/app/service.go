// Package app содержит use-cases Favorites/SavedSearch-сервиса.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"bozor/services/favorites-savedsearch/internal/domain"
)

// Пагинация избранного.
const (
	defaultLimit = 20
	maxLimit     = 100
)

// Store — доступ к БД избранного (реализуется repo.Repo).
type Store interface {
	Add(ctx context.Context, userID, adID string) (time.Time, error)
	Remove(ctx context.Context, userID, adID string) (bool, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]domain.Favorite, error)
	RemoveByAd(ctx context.Context, adID string) (int64, error)
}

// Service — use-cases избранного.
type Service struct {
	store Store
	log   *slog.Logger
}

// NewService создаёт сервис избранного.
func NewService(store Store, log *slog.Logger) *Service {
	return &Service{store: store, log: log}
}

// Add добавляет объявление в избранное владельца (идемпотентно). Существование
// объявления не проверяется (децентрализация): удалённые чистит потребитель
// bozor.ad.deleted.
func (s *Service) Add(ctx context.Context, userID, adID string) (domain.Favorite, error) {
	if !validUUID(adID) {
		return domain.Favorite{}, domain.ErrInvalidAdID
	}
	createdAt, err := s.store.Add(ctx, userID, adID)
	if err != nil {
		return domain.Favorite{}, err
	}
	return domain.Favorite{UserID: userID, AdID: adID, CreatedAt: createdAt}, nil
}

// Remove удаляет объявление из избранного владельца (идемпотентно).
func (s *Service) Remove(ctx context.Context, userID, adID string) error {
	if !validUUID(adID) {
		return domain.ErrInvalidAdID
	}
	_, err := s.store.Remove(ctx, userID, adID)
	return err
}

// List возвращает избранное владельца (пагинация).
func (s *Service) List(ctx context.Context, userID string, limit, offset int) ([]domain.Favorite, error) {
	return s.store.ListByUser(ctx, userID, clampLimit(limit), clampOffset(offset))
}

// RemoveByAd убирает объявление из всего избранного (обработчик bozor.ad.deleted).
func (s *Service) RemoveByAd(ctx context.Context, adID string) (int64, error) {
	return s.store.RemoveByAd(ctx, adID)
}

// validUUID сообщает, что строка — корректный UUID.
func validUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// clampLimit приводит размер страницы к [1, maxLimit] с дефолтом.
func clampLimit(n int) int {
	switch {
	case n <= 0:
		return defaultLimit
	case n > maxLimit:
		return maxLimit
	default:
		return n
	}
}

// clampOffset отсекает отрицательное смещение.
func clampOffset(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
