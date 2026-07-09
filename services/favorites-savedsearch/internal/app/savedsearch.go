package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"bozor/services/favorites-savedsearch/internal/domain"
)

// SavedSearchStore — доступ к БД сохранённых поисков (реализуется repo.Repo).
type SavedSearchStore interface {
	CreateSavedSearch(ctx context.Context, ss domain.SavedSearch) error
	CountSavedSearches(ctx context.Context, userID string) (int, error)
	ListSavedSearches(ctx context.Context, userID string) ([]domain.SavedSearch, error)
	DeleteSavedSearch(ctx context.Context, id, userID string) (bool, error)
}

// SavedSearchService — use-cases сохранённых поисков.
type SavedSearchService struct {
	store SavedSearchStore
	log   *slog.Logger
}

// NewSavedSearchService создаёт сервис сохранённых поисков.
func NewSavedSearchService(store SavedSearchStore, log *slog.Logger) *SavedSearchService {
	return &SavedSearchService{store: store, log: log}
}

// CreateSavedSearchInput — вход создания сохранённого поиска.
type CreateSavedSearchInput struct {
	Name          string
	Query         domain.SearchQuery
	NotifyEnabled bool
}

// Create создаёт сохранённый поиск владельца (валидация имени и лимита).
func (s *SavedSearchService) Create(ctx context.Context, userID string, in CreateSavedSearchInput) (domain.SavedSearch, error) {
	if err := domain.ValidateName(in.Name); err != nil {
		return domain.SavedSearch{}, err
	}
	n, err := s.store.CountSavedSearches(ctx, userID)
	if err != nil {
		return domain.SavedSearch{}, err
	}
	if n >= domain.MaxSavedSearches {
		return domain.SavedSearch{}, domain.ErrTooManySavedSearches
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.SavedSearch{}, fmt.Errorf("app: генерация id: %w", err)
	}
	ss := domain.SavedSearch{
		ID: id.String(), UserID: userID, Name: strings.TrimSpace(in.Name),
		Query: in.Query, NotifyEnabled: in.NotifyEnabled, CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateSavedSearch(ctx, ss); err != nil {
		return domain.SavedSearch{}, err
	}
	return ss, nil
}

// List возвращает сохранённые поиски владельца.
func (s *SavedSearchService) List(ctx context.Context, userID string) ([]domain.SavedSearch, error) {
	return s.store.ListSavedSearches(ctx, userID)
}

// Delete удаляет сохранённый поиск владельца (ErrSavedSearchNotFound, если нет).
func (s *SavedSearchService) Delete(ctx context.Context, id, userID string) error {
	existed, err := s.store.DeleteSavedSearch(ctx, id, userID)
	if err != nil {
		return err
	}
	if !existed {
		return domain.ErrSavedSearchNotFound
	}
	return nil
}
