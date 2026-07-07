// Package app содержит use-cases Catalog-сервиса.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/catalog/internal/domain"
)

const source = "catalog"

// Store — персистентность категорий (реализуется repo.Repo).
type Store interface {
	All(ctx context.Context) ([]domain.Category, error)
	GetByID(ctx context.Context, id string) (domain.Category, error)
	CreateWithEvent(ctx context.Context, c domain.Category, ev events.Envelope) error
	UpdateWithEvent(ctx context.Context, c domain.Category, ev events.Envelope) error
	DeleteWithEvent(ctx context.Context, id string, ev events.Envelope) error
}

// Cache — кеш дерева категорий (реализуется cache.TreeCache).
type Cache interface {
	Get(ctx context.Context) ([]byte, error)
	Set(ctx context.Context, data []byte) error
	Invalidate(ctx context.Context) error
}

// Service — use-cases каталога.
type Service struct {
	store Store
	cache Cache
	log   *slog.Logger
}

// NewService создаёт сервис каталога.
func NewService(store Store, cache Cache, log *slog.Logger) *Service {
	return &Service{store: store, cache: cache, log: log}
}

// TreeJSON возвращает JSON-ответ дерева категорий из кеша или из БД (с
// последующим кешированием). Кешируется готовое тело ответа.
func (s *Service) TreeJSON(ctx context.Context) ([]byte, error) {
	if data, err := s.cache.Get(ctx); err != nil {
		s.log.WarnContext(ctx, "кеш дерева недоступен", slog.String("error", err.Error()))
	} else if data != nil {
		return data, nil
	}

	cats, err := s.store.All(ctx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"categories": toViews(domain.BuildTree(cats))})
	if err != nil {
		return nil, fmt.Errorf("app: сериализация дерева: %w", err)
	}
	if err := s.cache.Set(ctx, body); err != nil {
		s.log.WarnContext(ctx, "не удалось закешировать дерево", slog.String("error", err.Error()))
	}
	return body, nil
}

// CreateInput — входные данные создания категории.
type CreateInput struct {
	ParentID  *string
	Slug      string
	NameUZ    string
	NameRU    string
	SortOrder int
	IsActive  bool
}

// Create добавляет категорию: вычисляет level/path от родителя, публикует
// bozor.category.created, инвалидирует кеш.
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Category, error) {
	level, path := 0, in.Slug
	if in.ParentID != nil {
		parent, err := s.store.GetByID(ctx, *in.ParentID)
		if errors.Is(err, domain.ErrCategoryNotFound) {
			return domain.Category{}, domain.ErrParentNotFound
		}
		if err != nil {
			return domain.Category{}, err
		}
		level = parent.Level + 1
		path = parent.Path + "/" + in.Slug
	}

	id, err := uuid.NewV7()
	if err != nil {
		return domain.Category{}, fmt.Errorf("app: генерация id: %w", err)
	}
	cat := domain.Category{
		ID: id.String(), ParentID: in.ParentID, Slug: in.Slug,
		NameUZ: in.NameUZ, NameRU: in.NameRU, Level: level, Path: path,
		SortOrder: in.SortOrder, IsActive: in.IsActive,
	}

	ev, err := s.event(events.SubjectCategoryCreated, cat)
	if err != nil {
		return domain.Category{}, err
	}
	if err := s.store.CreateWithEvent(ctx, cat, ev); err != nil {
		return domain.Category{}, err
	}
	s.invalidate(ctx)
	return cat, nil
}

// UpdateInput — изменяемые поля категории.
type UpdateInput struct {
	NameUZ    string
	NameRU    string
	SortOrder int
	IsActive  bool
}

// Update меняет имена/порядок/активность категории, публикует событие,
// инвалидирует кеш.
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (domain.Category, error) {
	cat, err := s.store.GetByID(ctx, id)
	if err != nil {
		return domain.Category{}, err
	}
	cat.NameUZ, cat.NameRU, cat.SortOrder, cat.IsActive = in.NameUZ, in.NameRU, in.SortOrder, in.IsActive

	ev, err := s.event(events.SubjectCategoryUpdated, cat)
	if err != nil {
		return domain.Category{}, err
	}
	if err := s.store.UpdateWithEvent(ctx, cat, ev); err != nil {
		return domain.Category{}, err
	}
	s.invalidate(ctx)
	return cat, nil
}

// Delete удаляет категорию (запрещено при наличии потомков), публикует событие,
// инвалидирует кеш.
func (s *Service) Delete(ctx context.Context, id string) error {
	cat, err := s.store.GetByID(ctx, id)
	if err != nil {
		return err
	}
	ev, err := s.event(events.SubjectCategoryDeleted, cat)
	if err != nil {
		return err
	}
	if err := s.store.DeleteWithEvent(ctx, id, ev); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// event собирает доменное событие категории.
func (s *Service) event(subject string, c domain.Category) (events.Envelope, error) {
	ev, err := events.New(subject, source, categoryEvent{
		CategoryID: c.ID, Slug: c.Slug, ParentID: c.ParentID,
	})
	if err != nil {
		return events.Envelope{}, fmt.Errorf("app: сборка события: %w", err)
	}
	return ev, nil
}

// invalidate сбрасывает кеш дерева (best-effort).
func (s *Service) invalidate(ctx context.Context) {
	if err := s.cache.Invalidate(ctx); err != nil {
		s.log.WarnContext(ctx, "не удалось инвалидировать кеш дерева", slog.String("error", err.Error()))
	}
}

// categoryEvent — payload события bozor.category.*.
type categoryEvent struct {
	CategoryID string  `json:"category_id"`
	Slug       string  `json:"slug"`
	ParentID   *string `json:"parent_id,omitempty"`
}
