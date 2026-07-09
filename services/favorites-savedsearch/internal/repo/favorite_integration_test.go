//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"bozor/pkg/shared/migrate"

	"bozor/services/favorites-savedsearch/internal/repo"
	"bozor/services/favorites-savedsearch/migrations"
)

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_favorites"),
		tcpostgres.WithUsername("bozor"),
		tcpostgres.WithPassword("bozor"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	_, err = migrate.Up(ctx, dsn, migrations.FS)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}

func TestRepo_AddIdempotentAndList(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid, ad1, ad2 := newID(t), newID(t), newID(t)

	t1, err := r.Add(ctx, uid, ad1)
	require.NoError(t, err)
	// Небольшая пауза, чтобы отличить created_at при повторе.
	time.Sleep(10 * time.Millisecond)
	t2, err := r.Add(ctx, uid, ad1)
	require.NoError(t, err)
	assert.Equal(t, t1.UnixNano(), t2.UnixNano(), "повторное добавление сохраняет исходное время")

	_, err = r.Add(ctx, uid, ad2)
	require.NoError(t, err)

	favs, err := r.ListByUser(ctx, uid, 10, 0)
	require.NoError(t, err)
	require.Len(t, favs, 2)
	assert.Equal(t, ad2, favs[0].AdID, "свежие сверху (created_at DESC)")
}

func TestRepo_Remove(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid, ad := newID(t), newID(t)
	_, err := r.Add(ctx, uid, ad)
	require.NoError(t, err)

	existed, err := r.Remove(ctx, uid, ad)
	require.NoError(t, err)
	assert.True(t, existed)

	existed, err = r.Remove(ctx, uid, ad)
	require.NoError(t, err)
	assert.False(t, existed, "повторное удаление — запись отсутствует")

	favs, err := r.ListByUser(ctx, uid, 10, 0)
	require.NoError(t, err)
	assert.Empty(t, favs)
}

func TestRepo_RemoveByAd(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	u1, u2, ad := newID(t), newID(t), newID(t)
	_, _ = r.Add(ctx, u1, ad)
	_, _ = r.Add(ctx, u2, ad)
	_, _ = r.Add(ctx, u1, newID(t)) // другое объявление — не трогаем

	n, err := r.RemoveByAd(ctx, ad)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "убрано из избранного обоих пользователей")

	favs, err := r.ListByUser(ctx, u1, 10, 0)
	require.NoError(t, err)
	assert.Len(t, favs, 1, "другое объявление осталось")
}

func TestRepo_ListPagination(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)
	for i := 0; i < 3; i++ {
		_, err := r.Add(ctx, uid, newID(t))
		require.NoError(t, err)
	}
	page, err := r.ListByUser(ctx, uid, 2, 0)
	require.NoError(t, err)
	assert.Len(t, page, 2)
	page2, err := r.ListByUser(ctx, uid, 2, 2)
	require.NoError(t, err)
	assert.Len(t, page2, 1)
}
