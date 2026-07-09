package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/favorites-savedsearch/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeStore — in-memory реализация Store (ключ user|ad).
type fakeStore struct {
	items      map[string]time.Time
	removeByAd int
}

func newFakeStore() *fakeStore { return &fakeStore{items: map[string]time.Time{}} }

func key(u, a string) string { return u + "|" + a }

func (f *fakeStore) Add(_ context.Context, userID, adID string) (time.Time, error) {
	k := key(userID, adID)
	if t, ok := f.items[k]; ok {
		return t, nil // идемпотентно — исходное время
	}
	t := time.Unix(int64(1700000000+len(f.items)), 0).UTC()
	f.items[k] = t
	return t, nil
}

func (f *fakeStore) Remove(_ context.Context, userID, adID string) (bool, error) {
	k := key(userID, adID)
	_, ok := f.items[k]
	delete(f.items, k)
	return ok, nil
}

func (f *fakeStore) ListByUser(_ context.Context, userID string, limit, offset int) ([]domain.Favorite, error) {
	var favs []domain.Favorite
	for k, t := range f.items {
		if k[:len(userID)] == userID {
			favs = append(favs, domain.Favorite{UserID: userID, AdID: k[len(userID)+1:], CreatedAt: t})
		}
	}
	return favs, nil
}

func (f *fakeStore) RemoveByAd(_ context.Context, adID string) (int64, error) {
	var n int64
	for k := range f.items {
		if k[len(k)-len(adID):] == adID {
			delete(f.items, k)
			n++
		}
	}
	f.removeByAd++
	return n, nil
}

const (
	uid = "019f4780-1111-7abc-8def-000000000001"
	aid = "019f4780-2222-7abc-8def-000000000002"
)

func TestService_Add_ValidatesUUID(t *testing.T) {
	svc := NewService(newFakeStore(), discardLogger())
	_, err := svc.Add(context.Background(), uid, "not-a-uuid")
	require.ErrorIs(t, err, domain.ErrInvalidAdID)
}

func TestService_Add_Idempotent(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, discardLogger())

	f1, err := svc.Add(context.Background(), uid, aid)
	require.NoError(t, err)
	f2, err := svc.Add(context.Background(), uid, aid)
	require.NoError(t, err)
	assert.Equal(t, f1.CreatedAt, f2.CreatedAt, "повторное добавление сохраняет время")
	assert.Len(t, store.items, 1)
}

func TestService_Remove_Idempotent(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, discardLogger())
	_, _ = svc.Add(context.Background(), uid, aid)

	require.NoError(t, svc.Remove(context.Background(), uid, aid))
	require.NoError(t, svc.Remove(context.Background(), uid, aid), "повторное удаление не ошибка")
	assert.Empty(t, store.items)
}

func TestService_Remove_InvalidUUID(t *testing.T) {
	err := NewService(newFakeStore(), discardLogger()).Remove(context.Background(), uid, "bad")
	require.ErrorIs(t, err, domain.ErrInvalidAdID)
}

func TestService_List_ClampsPaging(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, discardLogger())
	_, _ = svc.Add(context.Background(), uid, aid)

	favs, err := svc.List(context.Background(), uid, 0, -5)
	require.NoError(t, err)
	assert.Len(t, favs, 1)
}

func TestClampLimit(t *testing.T) {
	assert.Equal(t, defaultLimit, clampLimit(0))
	assert.Equal(t, maxLimit, clampLimit(1000))
	assert.Equal(t, 50, clampLimit(50))
	assert.Equal(t, 0, clampOffset(-1))
	assert.Equal(t, 10, clampOffset(10))
}

func TestService_RemoveByAd(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, discardLogger())
	_, _ = svc.Add(context.Background(), uid, aid)
	_, _ = svc.Add(context.Background(), "019f4780-3333-7abc-8def-000000000003", aid)

	n, err := svc.RemoveByAd(context.Background(), aid)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "объявление убрано из избранного обоих пользователей")
}
