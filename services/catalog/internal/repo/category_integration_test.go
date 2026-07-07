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

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/migrate"

	"bozor/services/catalog/internal/domain"
	"bozor/services/catalog/internal/repo"
	"bozor/services/catalog/migrations"
)

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_catalog"),
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

	// Схема применена вместе с сид-миграцией (00003). Тесты работают с чистыми
	// таблицами, чтобы не зависеть от сид-данных — вычищаем их.
	_, err = pool.Exec(ctx,
		`TRUNCATE category_attributes, attribute_options, attributes, categories, outbox RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return pool
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}

func ev(t *testing.T, subject, catID string) events.Envelope {
	t.Helper()
	e, err := events.New(subject, "catalog", map[string]any{"category_id": catID})
	require.NoError(t, err)
	return e
}

func TestCatalogRepo_CRUDAndEvents(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	rootID := newID(t)
	require.NoError(t, r.CreateWithEvent(ctx, domain.Category{
		ID: rootID, Slug: "electronics", NameUZ: "Elektronika", NameRU: "Электроника",
		Level: 0, Path: "electronics", IsActive: true,
	}, ev(t, events.SubjectCategoryCreated, rootID)))

	childID := newID(t)
	require.NoError(t, r.CreateWithEvent(ctx, domain.Category{
		ID: childID, ParentID: &rootID, Slug: "phones", NameUZ: "Telefonlar", NameRU: "Телефоны",
		Level: 1, Path: "electronics/phones", IsActive: true,
	}, ev(t, events.SubjectCategoryCreated, childID)))

	all, err := r.All(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "electronics", all[0].Slug, "корень идёт первым (level 0)")

	got, err := r.GetByID(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, "phones", got.Slug)
	require.NotNil(t, got.ParentID)
	assert.Equal(t, rootID, *got.ParentID)
	assert.Equal(t, "electronics/phones", got.Path)

	// Дубликат slug отклоняется.
	err = r.CreateWithEvent(ctx, domain.Category{
		ID: newID(t), Slug: "electronics", NameUZ: "x", NameRU: "x", Path: "electronics",
	}, ev(t, events.SubjectCategoryCreated, "dup"))
	assert.ErrorIs(t, err, domain.ErrSlugConflict)

	// Удаление категории с детьми запрещено.
	err = r.DeleteWithEvent(ctx, rootID, ev(t, events.SubjectCategoryDeleted, rootID))
	assert.ErrorIs(t, err, domain.ErrHasChildren)

	// Удаление листа проходит.
	require.NoError(t, r.DeleteWithEvent(ctx, childID, ev(t, events.SubjectCategoryDeleted, childID)))
	_, err = r.GetByID(ctx, childID)
	assert.ErrorIs(t, err, domain.ErrCategoryNotFound)

	// Теперь корень тоже удаляется.
	require.NoError(t, r.DeleteWithEvent(ctx, rootID, ev(t, events.SubjectCategoryDeleted, rootID)))

	// В outbox — по событию на каждую успешную операцию (2 create + 2 delete).
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM outbox").Scan(&n))
	assert.Equal(t, 4, n)
}

func TestCatalogRepo_UpdateNotFound(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	err := r.UpdateWithEvent(ctx, domain.Category{
		ID: newID(t), NameUZ: "x", NameRU: "x",
	}, ev(t, events.SubjectCategoryUpdated, "x"))
	assert.ErrorIs(t, err, domain.ErrCategoryNotFound)
}
