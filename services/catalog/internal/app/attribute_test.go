package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/catalog/internal/domain"
)

type fakeAttrStore struct {
	byID       map[string]domain.Attribute
	created    *domain.Attribute
	updated    *domain.Attribute
	deletedID  string
	linkCalled bool
	linkArgs   [3]string
	effective  []domain.EffectiveAttribute
	effCalls   int
}

func (f *fakeAttrStore) ListAttributes(context.Context) ([]domain.Attribute, error) {
	out := make([]domain.Attribute, 0, len(f.byID))
	for _, a := range f.byID {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeAttrStore) GetAttribute(_ context.Context, id string) (domain.Attribute, error) {
	a, ok := f.byID[id]
	if !ok {
		return domain.Attribute{}, domain.ErrAttributeNotFound
	}
	return a, nil
}

func (f *fakeAttrStore) CreateAttribute(_ context.Context, a domain.Attribute) error {
	f.created = &a
	return nil
}

func (f *fakeAttrStore) UpdateAttribute(_ context.Context, a domain.Attribute) error {
	f.updated = &a
	return nil
}

func (f *fakeAttrStore) DeleteAttribute(_ context.Context, id string) error {
	f.deletedID = id
	return nil
}

func (f *fakeAttrStore) EffectiveAttributes(context.Context, string) ([]domain.EffectiveAttribute, error) {
	f.effCalls++
	return f.effective, nil
}

func (f *fakeAttrStore) LinkAttribute(_ context.Context, categoryID, attributeID string, sortOrder int) error {
	f.linkCalled = true
	f.linkArgs = [3]string{categoryID, attributeID, ""}
	_ = sortOrder
	return nil
}

func (f *fakeAttrStore) UnlinkAttribute(context.Context, string, string) error { return nil }

type fakeCategoryLookup struct {
	exists map[string]domain.Category
}

func (f *fakeCategoryLookup) GetByID(_ context.Context, id string) (domain.Category, error) {
	c, ok := f.exists[id]
	if !ok {
		return domain.Category{}, domain.ErrCategoryNotFound
	}
	return c, nil
}

type fakeAttrCache struct {
	data        map[string][]byte
	setCount    int
	invalidated int
}

func newFakeAttrCache() *fakeAttrCache { return &fakeAttrCache{data: map[string][]byte{}} }

func (f *fakeAttrCache) Get(_ context.Context, id string) ([]byte, error) { return f.data[id], nil }
func (f *fakeAttrCache) Set(_ context.Context, id string, d []byte) error {
	f.data[id] = d
	f.setCount++
	return nil
}
func (f *fakeAttrCache) Invalidate(context.Context) error {
	f.data = map[string][]byte{}
	f.invalidated++
	return nil
}

func newAttrSvc(store *fakeAttrStore, cats *fakeCategoryLookup) *AttributeService {
	return NewAttributeService(store, cats, newFakeAttrCache(), discardLogger())
}

func TestCreateAttribute_InvalidType(t *testing.T) {
	_, err := newAttrSvc(&fakeAttrStore{}, &fakeCategoryLookup{}).Create(context.Background(), CreateAttributeInput{
		Slug: "x", NameUZ: "X", NameRU: "X", Type: "geo",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidAttributeType)
}

func TestCreateAttribute_EnumRequiresOptions(t *testing.T) {
	_, err := newAttrSvc(&fakeAttrStore{}, &fakeCategoryLookup{}).Create(context.Background(), CreateAttributeInput{
		Slug: "color", NameUZ: "Rang", NameRU: "Цвет", Type: domain.TypeEnum,
	})
	assert.ErrorIs(t, err, domain.ErrEnumRequiresOptions)
}

func TestCreateAttribute_OptionsForbiddenForNonEnum(t *testing.T) {
	_, err := newAttrSvc(&fakeAttrStore{}, &fakeCategoryLookup{}).Create(context.Background(), CreateAttributeInput{
		Slug: "area", NameUZ: "Maydon", NameRU: "Площадь", Type: domain.TypeDecimal,
		Options: []OptionInput{{Slug: "s", NameUZ: "S", NameRU: "S"}},
	})
	assert.ErrorIs(t, err, domain.ErrOptionsForbidden)
}

func TestCreateAttribute_EnumAssignsIDs(t *testing.T) {
	store := &fakeAttrStore{}
	attr, err := newAttrSvc(store, &fakeCategoryLookup{}).Create(context.Background(), CreateAttributeInput{
		Slug: "deal", NameUZ: "Bitim", NameRU: "Сделка", Type: domain.TypeEnum,
		Options: []OptionInput{
			{Slug: "sale", NameUZ: "Sotish", NameRU: "Продажа", SortOrder: 1},
			{Slug: "rent", NameUZ: "Ijara", NameRU: "Аренда", SortOrder: 2},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, store.created)
	assert.NotEmpty(t, attr.ID)
	require.Len(t, attr.Options, 2)
	assert.NotEmpty(t, attr.Options[0].ID)
	assert.NotEqual(t, attr.Options[0].ID, attr.Options[1].ID)
}

func TestCreateAttribute_EmptyUnitBecomesNil(t *testing.T) {
	empty := "   "
	store := &fakeAttrStore{}
	attr, err := newAttrSvc(store, &fakeCategoryLookup{}).Create(context.Background(), CreateAttributeInput{
		Slug: "rooms", NameUZ: "Xona", NameRU: "Комнаты", Type: domain.TypeInt, Unit: &empty,
	})
	require.NoError(t, err)
	assert.Nil(t, attr.Unit, "пустая единица измерения → nil (NULL в БД)")
}

func TestEffective_CategoryNotFound(t *testing.T) {
	_, err := newAttrSvc(&fakeAttrStore{}, &fakeCategoryLookup{}).Effective(context.Background(), "missing")
	assert.ErrorIs(t, err, domain.ErrCategoryNotFound)
}

func TestEffective_ReturnsStoreResult(t *testing.T) {
	cats := &fakeCategoryLookup{exists: map[string]domain.Category{"c1": {ID: "c1"}}}
	store := &fakeAttrStore{effective: []domain.EffectiveAttribute{
		{Attribute: domain.Attribute{ID: "a1", Slug: "deal"}, Inherited: true, SourceID: "root"},
	}}
	got, err := newAttrSvc(store, cats).Effective(context.Background(), "c1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Inherited)
}

func TestLink_RequiresCategoryAndAttribute(t *testing.T) {
	cats := &fakeCategoryLookup{exists: map[string]domain.Category{"c1": {ID: "c1"}}}
	store := &fakeAttrStore{byID: map[string]domain.Attribute{"a1": {ID: "a1"}}}
	svc := newAttrSvc(store, cats)

	// Категория отсутствует.
	err := svc.Link(context.Background(), "missing", "a1", 0)
	assert.ErrorIs(t, err, domain.ErrCategoryNotFound)

	// Атрибут отсутствует.
	err = svc.Link(context.Background(), "c1", "missing", 0)
	assert.ErrorIs(t, err, domain.ErrAttributeNotFound)

	// Обе существуют — привязка проходит.
	err = svc.Link(context.Background(), "c1", "a1", 3)
	require.NoError(t, err)
	assert.True(t, store.linkCalled)
	assert.Equal(t, "c1", store.linkArgs[0])
	assert.Equal(t, "a1", store.linkArgs[1])
}

func TestUpdateAttribute_ValidatesOptionsAgainstType(t *testing.T) {
	store := &fakeAttrStore{byID: map[string]domain.Attribute{
		"a1": {ID: "a1", Slug: "area", Type: domain.TypeDecimal},
	}}
	_, err := newAttrSvc(store, &fakeCategoryLookup{}).Update(context.Background(), "a1", UpdateAttributeInput{
		NameUZ: "Maydon", NameRU: "Площадь",
		Options: []OptionInput{{Slug: "s", NameUZ: "S", NameRU: "S"}},
	})
	assert.ErrorIs(t, err, domain.ErrOptionsForbidden)
}

func TestUpdateAttribute_NotFound(t *testing.T) {
	_, err := newAttrSvc(&fakeAttrStore{byID: map[string]domain.Attribute{}}, &fakeCategoryLookup{}).
		Update(context.Background(), "nope", UpdateAttributeInput{NameUZ: "X", NameRU: "X"})
	assert.True(t, errors.Is(err, domain.ErrAttributeNotFound))
}

func TestEffectiveJSON_CacheMissThenHit(t *testing.T) {
	cats := &fakeCategoryLookup{exists: map[string]domain.Category{"c1": {ID: "c1"}}}
	store := &fakeAttrStore{effective: []domain.EffectiveAttribute{
		{Attribute: domain.Attribute{ID: "a1", Slug: "brand", Type: domain.TypeEnum}, Inherited: true, SourceID: "root"},
	}}
	c := newFakeAttrCache()
	svc := NewAttributeService(store, cats, c, discardLogger())

	// Промах: считаем из БД, кешируем.
	body, err := svc.EffectiveJSON(context.Background(), "c1")
	require.NoError(t, err)
	assert.Contains(t, string(body), `"brand"`)
	assert.Contains(t, string(body), `"inherited":true`)
	assert.Equal(t, 1, store.effCalls)
	assert.Equal(t, 1, c.setCount)

	// Попадание: БД не трогаем.
	body2, err := svc.EffectiveJSON(context.Background(), "c1")
	require.NoError(t, err)
	assert.Equal(t, body, body2)
	assert.Equal(t, 1, store.effCalls, "второй раз читаем из кеша")
}

func TestEffectiveJSON_CategoryNotFound(t *testing.T) {
	c := newFakeAttrCache()
	svc := NewAttributeService(&fakeAttrStore{}, &fakeCategoryLookup{}, c, discardLogger())
	_, err := svc.EffectiveJSON(context.Background(), "missing")
	assert.ErrorIs(t, err, domain.ErrCategoryNotFound)
	assert.Equal(t, 0, c.setCount, "для несуществующей категории кеш не пишется")
}

func TestMutations_InvalidateCache(t *testing.T) {
	cats := &fakeCategoryLookup{exists: map[string]domain.Category{"c1": {ID: "c1"}}}
	store := &fakeAttrStore{byID: map[string]domain.Attribute{
		"a1": {ID: "a1", Slug: "brand", Type: domain.TypeString},
	}}
	c := newFakeAttrCache()
	svc := NewAttributeService(store, cats, c, discardLogger())

	require.NoError(t, svc.Link(context.Background(), "c1", "a1", 0))
	assert.Equal(t, 1, c.invalidated, "link сбрасывает кеш")

	require.NoError(t, svc.Unlink(context.Background(), "c1", "a1"))
	assert.Equal(t, 2, c.invalidated, "unlink сбрасывает кеш")

	_, err := svc.Update(context.Background(), "a1", UpdateAttributeInput{NameUZ: "X", NameRU: "X"})
	require.NoError(t, err)
	assert.Equal(t, 3, c.invalidated, "update сбрасывает кеш")

	require.NoError(t, svc.Delete(context.Background(), "a1"))
	assert.Equal(t, 4, c.invalidated, "delete сбрасывает кеш")
}
