//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"bozor/pkg/shared/migrate"

	"bozor/services/location/internal/domain"
	"bozor/services/location/internal/repo"
	"bozor/services/location/migrations"
)

// startDB поднимает postgres и применяет миграции Location (схему + сид).
func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_location"),
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

func findRegion(regions []domain.Region, slug string) (domain.Region, bool) {
	for _, r := range regions {
		if r.Slug == slug {
			return r, true
		}
	}
	return domain.Region{}, false
}

func TestLocationRepo_SeededData(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	regions, err := r.Regions(ctx)
	require.NoError(t, err)
	assert.Len(t, regions, 14, "14 первичных регионов Узбекистана")
	// Первым идёт город Ташкент (sort_order = 1).
	assert.Equal(t, "toshkent-shahri", regions[0].Slug)
	require.NotNil(t, regions[0].Latitude)
	assert.InDelta(t, 41.3111, *regions[0].Latitude, 0.001)

	// Апостроф в узбекском названии сохранён корректно.
	fargona, ok := findRegion(regions, "fargona")
	require.True(t, ok)
	assert.Equal(t, "Farg'ona viloyati", fargona.NameUZ)

	// В городе Ташкенте — 12 районов.
	cities, err := r.CitiesByRegion(ctx, 1)
	require.NoError(t, err)
	assert.Len(t, cities, 12, "12 районов города Ташкента")
	for _, c := range cities {
		assert.Equal(t, 1, c.RegionID)
	}

	var total int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM cities").Scan(&total))
	assert.Equal(t, 56, total, "всего засеяно 56 городов/районов")
}

func TestLocationRepo_RegionExists(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	exists, err := r.RegionExists(ctx, 11) // Samarqand
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = r.RegionExists(ctx, 999)
	require.NoError(t, err)
	assert.False(t, exists)

	// Города несуществующего региона — пустой список без ошибки.
	cities, err := r.CitiesByRegion(ctx, 999)
	require.NoError(t, err)
	assert.Empty(t, cities)
}
