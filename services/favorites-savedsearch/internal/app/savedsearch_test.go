package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/favorites-savedsearch/internal/domain"
)

// fakeSavedStore — in-memory реализация SavedSearchStore.
type fakeSavedStore struct {
	items map[string]domain.SavedSearch
}

func newFakeSavedStore() *fakeSavedStore {
	return &fakeSavedStore{items: map[string]domain.SavedSearch{}}
}

func (f *fakeSavedStore) CreateSavedSearch(_ context.Context, ss domain.SavedSearch) error {
	f.items[ss.ID] = ss
	return nil
}

func (f *fakeSavedStore) CountSavedSearches(_ context.Context, userID string) (int, error) {
	n := 0
	for _, ss := range f.items {
		if ss.UserID == userID {
			n++
		}
	}
	return n, nil
}

func (f *fakeSavedStore) ListSavedSearches(_ context.Context, userID string) ([]domain.SavedSearch, error) {
	var out []domain.SavedSearch
	for _, ss := range f.items {
		if ss.UserID == userID {
			out = append(out, ss)
		}
	}
	return out, nil
}

func (f *fakeSavedStore) DeleteSavedSearch(_ context.Context, id, userID string) (bool, error) {
	ss, ok := f.items[id]
	if !ok || ss.UserID != userID {
		return false, nil
	}
	delete(f.items, id)
	return true, nil
}

func TestSavedSearchService_Create(t *testing.T) {
	store := newFakeSavedStore()
	svc := NewSavedSearchService(store, discardLogger())

	ss, err := svc.Create(context.Background(), "u1", CreateSavedSearchInput{
		Name:          "  Машины  ",
		Query:         domain.SearchQuery{CategoryID: "cars", RegionID: 14},
		NotifyEnabled: true,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, ss.ID)
	assert.Equal(t, "Машины", ss.Name, "имя обрезано")
	assert.Equal(t, "cars", ss.Query.CategoryID)
	assert.Len(t, store.items, 1)
}

func TestSavedSearchService_Create_InvalidName(t *testing.T) {
	svc := NewSavedSearchService(newFakeSavedStore(), discardLogger())
	_, err := svc.Create(context.Background(), "u1", CreateSavedSearchInput{Name: "  "})
	require.ErrorIs(t, err, domain.ErrEmptyName)
}

func TestSavedSearchService_Create_LimitExceeded(t *testing.T) {
	store := newFakeSavedStore()
	for i := 0; i < domain.MaxSavedSearches; i++ {
		store.items[string(rune('a'+i))+"-id"] = domain.SavedSearch{ID: string(rune('a'+i)) + "-id", UserID: "u1"}
	}
	svc := NewSavedSearchService(store, discardLogger())
	_, err := svc.Create(context.Background(), "u1", CreateSavedSearchInput{Name: "ещё один"})
	require.ErrorIs(t, err, domain.ErrTooManySavedSearches)
}

func TestSavedSearchService_Delete(t *testing.T) {
	store := newFakeSavedStore()
	store.items["s1"] = domain.SavedSearch{ID: "s1", UserID: "u1"}
	svc := NewSavedSearchService(store, discardLogger())

	require.NoError(t, svc.Delete(context.Background(), "s1", "u1"))
	assert.Empty(t, store.items)

	// Повторное удаление / чужой — ErrSavedSearchNotFound.
	require.ErrorIs(t, svc.Delete(context.Background(), "s1", "u1"), domain.ErrSavedSearchNotFound)
	store.items["s2"] = domain.SavedSearch{ID: "s2", UserID: "u2"}
	require.ErrorIs(t, svc.Delete(context.Background(), "s2", "u1"), domain.ErrSavedSearchNotFound)
}

func TestSavedSearchService_List(t *testing.T) {
	store := newFakeSavedStore()
	store.items["s1"] = domain.SavedSearch{ID: "s1", UserID: "u1"}
	store.items["s2"] = domain.SavedSearch{ID: "s2", UserID: "u2"}
	svc := NewSavedSearchService(store, discardLogger())

	list, err := svc.List(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "s1", list[0].ID)
}
