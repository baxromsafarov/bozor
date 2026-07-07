package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"bozor/services/catalog/internal/domain"
)

// AttributeStore — персистентность атрибутов и привязок (реализуется repo.Repo).
type AttributeStore interface {
	ListAttributes(ctx context.Context) ([]domain.Attribute, error)
	GetAttribute(ctx context.Context, id string) (domain.Attribute, error)
	CreateAttribute(ctx context.Context, a domain.Attribute) error
	UpdateAttribute(ctx context.Context, a domain.Attribute) error
	DeleteAttribute(ctx context.Context, id string) error
	EffectiveAttributes(ctx context.Context, categoryID string) ([]domain.EffectiveAttribute, error)
	LinkAttribute(ctx context.Context, categoryID, attributeID string, sortOrder int) error
	UnlinkAttribute(ctx context.Context, categoryID, attributeID string) error
}

// CategoryLookup — проверка существования категории (реализуется repo.Repo).
type CategoryLookup interface {
	GetByID(ctx context.Context, id string) (domain.Category, error)
}

// AttrCache — кеш ответов эффективных атрибутов (реализуется cache.AttrCache).
type AttrCache interface {
	Get(ctx context.Context, categoryID string) ([]byte, error)
	Set(ctx context.Context, categoryID string, data []byte) error
	Invalidate(ctx context.Context) error
}

// AttributeService — use-cases атрибутов каталога.
type AttributeService struct {
	attrs AttributeStore
	cats  CategoryLookup
	cache AttrCache
	log   *slog.Logger
}

// NewAttributeService создаёт сервис атрибутов.
func NewAttributeService(attrs AttributeStore, cats CategoryLookup, cache AttrCache, log *slog.Logger) *AttributeService {
	return &AttributeService{attrs: attrs, cats: cats, cache: cache, log: log}
}

// List возвращает все определения атрибутов.
func (s *AttributeService) List(ctx context.Context) ([]domain.Attribute, error) {
	return s.attrs.ListAttributes(ctx)
}

// Get возвращает атрибут по id.
func (s *AttributeService) Get(ctx context.Context, id string) (domain.Attribute, error) {
	return s.attrs.GetAttribute(ctx, id)
}

// OptionInput — вариант enum-значения на входе.
type OptionInput struct {
	Slug      string
	NameUZ    string
	NameRU    string
	SortOrder int
}

// CreateAttributeInput — входные данные создания атрибута.
type CreateAttributeInput struct {
	Slug         string
	NameUZ       string
	NameRU       string
	Type         domain.AttributeType
	Unit         *string
	IsRequired   bool
	IsFilterable bool
	Options      []OptionInput
}

// Create добавляет атрибут (с вариантами для enum).
func (s *AttributeService) Create(ctx context.Context, in CreateAttributeInput) (domain.Attribute, error) {
	if !in.Type.Valid() {
		return domain.Attribute{}, domain.ErrInvalidAttributeType
	}
	if err := validateOptions(in.Type, len(in.Options)); err != nil {
		return domain.Attribute{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return domain.Attribute{}, fmt.Errorf("app: генерация id атрибута: %w", err)
	}
	attr := domain.Attribute{
		ID: id.String(), Slug: in.Slug, NameUZ: in.NameUZ, NameRU: in.NameRU,
		Type: in.Type, Unit: normalizeUnit(in.Unit),
		IsRequired: in.IsRequired, IsFilterable: in.IsFilterable,
	}
	attr.Options, err = buildOptions(in.Options)
	if err != nil {
		return domain.Attribute{}, err
	}

	if err := s.attrs.CreateAttribute(ctx, attr); err != nil {
		return domain.Attribute{}, err
	}
	return attr, nil
}

// UpdateAttributeInput — изменяемые поля атрибута (тип и slug неизменяемы).
type UpdateAttributeInput struct {
	NameUZ       string
	NameRU       string
	Unit         *string
	IsRequired   bool
	IsFilterable bool
	Options      []OptionInput
}

// Update меняет имена/единицу/флаги атрибута и полностью заменяет варианты.
func (s *AttributeService) Update(ctx context.Context, id string, in UpdateAttributeInput) (domain.Attribute, error) {
	cur, err := s.attrs.GetAttribute(ctx, id)
	if err != nil {
		return domain.Attribute{}, err
	}
	if err := validateOptions(cur.Type, len(in.Options)); err != nil {
		return domain.Attribute{}, err
	}

	cur.NameUZ, cur.NameRU = in.NameUZ, in.NameRU
	cur.Unit = normalizeUnit(in.Unit)
	cur.IsRequired, cur.IsFilterable = in.IsRequired, in.IsFilterable
	cur.Options, err = buildOptions(in.Options)
	if err != nil {
		return domain.Attribute{}, err
	}

	if err := s.attrs.UpdateAttribute(ctx, cur); err != nil {
		return domain.Attribute{}, err
	}
	s.invalidate(ctx) // данные атрибута изменились везде, где он привязан
	return cur, nil
}

// Delete удаляет атрибут (варианты и привязки уходят каскадом).
func (s *AttributeService) Delete(ctx context.Context, id string) error {
	if err := s.attrs.DeleteAttribute(ctx, id); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// Effective возвращает эффективные атрибуты категории (собственные + унаследованные).
func (s *AttributeService) Effective(ctx context.Context, categoryID string) ([]domain.EffectiveAttribute, error) {
	if _, err := s.cats.GetByID(ctx, categoryID); err != nil {
		return nil, err
	}
	return s.attrs.EffectiveAttributes(ctx, categoryID)
}

// EffectiveJSON возвращает готовый JSON-ответ эффективных атрибутов из кеша или
// из БД (с последующим кешированием). Кешируется тело ответа по категории.
func (s *AttributeService) EffectiveJSON(ctx context.Context, categoryID string) ([]byte, error) {
	if _, err := s.cats.GetByID(ctx, categoryID); err != nil {
		return nil, err
	}
	if data, err := s.cache.Get(ctx, categoryID); err != nil {
		s.log.WarnContext(ctx, "кеш атрибутов недоступен", slog.String("error", err.Error()))
	} else if data != nil {
		return data, nil
	}

	items, err := s.attrs.EffectiveAttributes(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"attributes": toEffectiveViews(items)})
	if err != nil {
		return nil, fmt.Errorf("app: сериализация атрибутов: %w", err)
	}
	if err := s.cache.Set(ctx, categoryID, body); err != nil {
		s.log.WarnContext(ctx, "не удалось закешировать атрибуты", slog.String("error", err.Error()))
	}
	return body, nil
}

// Link привязывает атрибут к категории (обе сущности должны существовать).
func (s *AttributeService) Link(ctx context.Context, categoryID, attributeID string, sortOrder int) error {
	if _, err := s.cats.GetByID(ctx, categoryID); err != nil {
		return err
	}
	if _, err := s.attrs.GetAttribute(ctx, attributeID); err != nil {
		return err
	}
	if err := s.attrs.LinkAttribute(ctx, categoryID, attributeID, sortOrder); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// Unlink снимает привязку атрибута с категории.
func (s *AttributeService) Unlink(ctx context.Context, categoryID, attributeID string) error {
	if err := s.attrs.UnlinkAttribute(ctx, categoryID, attributeID); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// invalidate сбрасывает кеш эффективных атрибутов (best-effort).
func (s *AttributeService) invalidate(ctx context.Context) {
	if err := s.cache.Invalidate(ctx); err != nil {
		s.log.WarnContext(ctx, "не удалось инвалидировать кеш атрибутов", slog.String("error", err.Error()))
	}
}

// validateOptions проверяет соответствие набора вариантов типу атрибута.
func validateOptions(t domain.AttributeType, count int) error {
	if t == domain.TypeEnum && count == 0 {
		return domain.ErrEnumRequiresOptions
	}
	if t != domain.TypeEnum && count > 0 {
		return domain.ErrOptionsForbidden
	}
	return nil
}

// buildOptions присваивает вариантам UUID и переносит их в доменную модель.
func buildOptions(in []OptionInput) ([]domain.AttributeOption, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]domain.AttributeOption, len(in))
	for i, o := range in {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("app: генерация id варианта: %w", err)
		}
		out[i] = domain.AttributeOption{
			ID: id.String(), Slug: o.Slug, NameUZ: o.NameUZ, NameRU: o.NameRU, SortOrder: o.SortOrder,
		}
	}
	return out, nil
}

// normalizeUnit убирает пустую строку единицы измерения (→ NULL в БД).
func normalizeUnit(u *string) *string {
	if u == nil {
		return nil
	}
	if strings.TrimSpace(*u) == "" {
		return nil
	}
	return u
}
