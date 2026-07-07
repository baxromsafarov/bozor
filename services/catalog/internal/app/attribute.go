package app

import (
	"context"
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

// AttributeService — use-cases атрибутов каталога.
type AttributeService struct {
	attrs AttributeStore
	cats  CategoryLookup
	log   *slog.Logger
}

// NewAttributeService создаёт сервис атрибутов.
func NewAttributeService(attrs AttributeStore, cats CategoryLookup, log *slog.Logger) *AttributeService {
	return &AttributeService{attrs: attrs, cats: cats, log: log}
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
	return cur, nil
}

// Delete удаляет атрибут (варианты и привязки уходят каскадом).
func (s *AttributeService) Delete(ctx context.Context, id string) error {
	return s.attrs.DeleteAttribute(ctx, id)
}

// Effective возвращает эффективные атрибуты категории (собственные + унаследованные).
func (s *AttributeService) Effective(ctx context.Context, categoryID string) ([]domain.EffectiveAttribute, error) {
	if _, err := s.cats.GetByID(ctx, categoryID); err != nil {
		return nil, err
	}
	return s.attrs.EffectiveAttributes(ctx, categoryID)
}

// Link привязывает атрибут к категории (обе сущности должны существовать).
func (s *AttributeService) Link(ctx context.Context, categoryID, attributeID string, sortOrder int) error {
	if _, err := s.cats.GetByID(ctx, categoryID); err != nil {
		return err
	}
	if _, err := s.attrs.GetAttribute(ctx, attributeID); err != nil {
		return err
	}
	return s.attrs.LinkAttribute(ctx, categoryID, attributeID, sortOrder)
}

// Unlink снимает привязку атрибута с категории.
func (s *AttributeService) Unlink(ctx context.Context, categoryID, attributeID string) error {
	return s.attrs.UnlinkAttribute(ctx, categoryID, attributeID)
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
