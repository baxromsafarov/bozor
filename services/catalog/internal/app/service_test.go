package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/catalog/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	all       []domain.Category
	allCalled bool
	byID      map[string]domain.Category
	created   *domain.Category
	updated   *domain.Category
	deletedID string
	events    []events.Envelope
	createErr error
}

func (f *fakeStore) All(context.Context) ([]domain.Category, error) {
	f.allCalled = true
	return f.all, nil
}

func (f *fakeStore) GetByID(_ context.Context, id string) (domain.Category, error) {
	c, ok := f.byID[id]
	if !ok {
		return domain.Category{}, domain.ErrCategoryNotFound
	}
	return c, nil
}

func (f *fakeStore) CreateWithEvent(_ context.Context, c domain.Category, ev events.Envelope) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = &c
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStore) UpdateWithEvent(_ context.Context, c domain.Category, ev events.Envelope) error {
	f.updated = &c
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStore) DeleteWithEvent(_ context.Context, id string, ev events.Envelope) error {
	f.deletedID = id
	f.events = append(f.events, ev)
	return nil
}

type fakeCache struct {
	data        []byte
	setCount    int
	invalidated int
}

func (f *fakeCache) Get(context.Context) ([]byte, error) { return f.data, nil }
func (f *fakeCache) Set(_ context.Context, d []byte) error {
	f.data = d
	f.setCount++
	return nil
}
func (f *fakeCache) Invalidate(context.Context) error {
	f.data = nil
	f.invalidated++
	return nil
}

func TestTreeJSON_CacheHit(t *testing.T) {
	store := &fakeStore{}
	cache := &fakeCache{data: []byte(`{"categories":[]}`)}
	body, err := NewService(store, cache, discardLogger()).TreeJSON(context.Background())
	require.NoError(t, err)
	assert.JSONEq(t, `{"categories":[]}`, string(body))
	assert.False(t, store.allCalled, "при попадании в кеш БД не читается")
}

func TestTreeJSON_CacheMissBuildsAndCaches(t *testing.T) {
	store := &fakeStore{all: []domain.Category{
		{ID: "1", Slug: "electronics", NameUZ: "Elektronika", NameRU: "Электроника", Level: 0},
	}}
	cache := &fakeCache{}
	body, err := NewService(store, cache, discardLogger()).TreeJSON(context.Background())
	require.NoError(t, err)
	assert.True(t, store.allCalled)
	assert.Contains(t, string(body), "electronics")
	assert.Equal(t, 1, cache.setCount, "результат закеширован")

	// Ответ — валидный JSON с деревом.
	var parsed struct {
		Categories []map[string]any `json:"categories"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Len(t, parsed.Categories, 1)
}

func TestCreate_RootComputesPath(t *testing.T) {
	store := &fakeStore{}
	cache := &fakeCache{}
	cat, err := NewService(store, cache, discardLogger()).Create(context.Background(), CreateInput{
		Slug: "electronics", NameUZ: "Elektronika", NameRU: "Электроника", IsActive: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, cat.Level)
	assert.Equal(t, "electronics", cat.Path)
	require.NotNil(t, store.created)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectCategoryCreated, store.events[0].Type)
	assert.Equal(t, 1, cache.invalidated, "кеш сброшен после создания")
}

func TestCreate_ChildInheritsPath(t *testing.T) {
	parentID := "p1"
	store := &fakeStore{byID: map[string]domain.Category{
		parentID: {ID: parentID, Slug: "electronics", Level: 0, Path: "electronics"},
	}}
	cat, err := NewService(store, &fakeCache{}, discardLogger()).Create(context.Background(), CreateInput{
		ParentID: &parentID, Slug: "phones", NameUZ: "Telefonlar", NameRU: "Телефоны", IsActive: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, cat.Level)
	assert.Equal(t, "electronics/phones", cat.Path)
}

func TestCreate_ParentNotFound(t *testing.T) {
	missing := "nope"
	store := &fakeStore{byID: map[string]domain.Category{}}
	_, err := NewService(store, &fakeCache{}, discardLogger()).Create(context.Background(), CreateInput{
		ParentID: &missing, Slug: "x", NameUZ: "X", NameRU: "X",
	})
	assert.ErrorIs(t, err, domain.ErrParentNotFound)
}

func TestUpdate_PublishesEventAndInvalidates(t *testing.T) {
	id := "c1"
	store := &fakeStore{byID: map[string]domain.Category{
		id: {ID: id, Slug: "phones", Level: 1, Path: "electronics/phones"},
	}}
	cache := &fakeCache{}
	cat, err := NewService(store, cache, discardLogger()).Update(context.Background(), id, UpdateInput{
		NameUZ: "Yangi", NameRU: "Новое", SortOrder: 5, IsActive: false,
	})
	require.NoError(t, err)
	assert.Equal(t, "Новое", cat.NameRU)
	assert.Equal(t, "electronics/phones", cat.Path, "path не меняется при апдейте")
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectCategoryUpdated, store.events[0].Type)
	assert.Equal(t, 1, cache.invalidated)
}

func TestDelete_PublishesEventAndInvalidates(t *testing.T) {
	id := "c1"
	store := &fakeStore{byID: map[string]domain.Category{id: {ID: id, Slug: "phones"}}}
	cache := &fakeCache{}
	err := NewService(store, cache, discardLogger()).Delete(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, id, store.deletedID)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectCategoryDeleted, store.events[0].Type)
	assert.Equal(t, 1, cache.invalidated)
}

func TestDelete_NotFound(t *testing.T) {
	store := &fakeStore{byID: map[string]domain.Category{}}
	err := NewService(store, &fakeCache{}, discardLogger()).Delete(context.Background(), "nope")
	assert.ErrorIs(t, err, domain.ErrCategoryNotFound)
}
