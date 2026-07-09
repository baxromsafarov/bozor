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

	"bozor/services/listing/internal/domain"
	"bozor/services/listing/internal/repo"
	"bozor/services/listing/migrations"
)

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_listing"),
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

func createdEvent(t *testing.T, adID string) events.Envelope {
	t.Helper()
	e, err := events.New(events.SubjectAdCreated, "listing", map[string]any{"ad_id": adID})
	require.NoError(t, err)
	return e
}

func TestListingRepo_CreateGetWithChildren(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	id := newID(t)
	now := time.Now().UTC()
	city := int64(5)
	ad := domain.Ad{
		ID: id, UserID: newID(t), CategoryID: newID(t), Title: "BMW X5",
		Description: "отличное состояние", Price: 500000000, Currency: "UZS",
		RegionID: 1, CityID: &city, Status: domain.StatusDraft, PhoneDisplay: true,
		CreatedAt: now, UpdatedAt: now,
		Attributes: []domain.AdAttributeValue{
			{AttributeSlug: "brand", Value: "bmw"},
			{AttributeSlug: "year", Value: "2015"},
		},
		Images: []domain.AdImage{
			{MediaID: newID(t), SortOrder: 0, IsCover: true},
			{MediaID: newID(t), SortOrder: 1, IsCover: false},
		},
	}
	require.NoError(t, r.CreateWithEvent(ctx, ad, createdEvent(t, id)))

	got, err := r.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, ad.UserID, got.UserID)
	assert.Equal(t, "BMW X5", got.Title)
	assert.Equal(t, int64(500000000), got.Price)
	assert.Equal(t, domain.StatusDraft, got.Status)
	require.NotNil(t, got.CityID)
	assert.Equal(t, int64(5), *got.CityID)

	require.Len(t, got.Attributes, 2, "атрибуты загружены (по алфавиту slug)")
	assert.Equal(t, "brand", got.Attributes[0].AttributeSlug)
	assert.Equal(t, "bmw", got.Attributes[0].Value)

	require.Len(t, got.Images, 2, "изображения загружены (по sort_order)")
	assert.True(t, got.Images[0].IsCover)
	assert.Equal(t, 1, got.Images[1].SortOrder)

	// outbox: одно событие bozor.ad.created.
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM outbox").Scan(&n))
	assert.Equal(t, 1, n)
}

func TestListingRepo_GetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	_, err := r.GetByID(ctx, newID(t))
	assert.ErrorIs(t, err, domain.ErrAdNotFound)
}

func TestListingRepo_DeleteCascadesChildren(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	id := newID(t)
	now := time.Now().UTC()
	ad := domain.Ad{
		ID: id, UserID: newID(t), CategoryID: newID(t), Title: "Тест", Price: 1, Currency: "UZS",
		RegionID: 1, Status: domain.StatusDraft, CreatedAt: now, UpdatedAt: now,
		Attributes: []domain.AdAttributeValue{{AttributeSlug: "brand", Value: "bmw"}},
		Images:     []domain.AdImage{{MediaID: newID(t), IsCover: true}},
	}
	require.NoError(t, r.CreateWithEvent(ctx, ad, createdEvent(t, id)))

	// Удаление объявления каскадит на значения атрибутов и изображения (FK CASCADE).
	_, err := pool.Exec(ctx, "DELETE FROM ads WHERE id = $1", id)
	require.NoError(t, err)

	var attrs, imgs int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM ad_attribute_values WHERE ad_id=$1", id).Scan(&attrs))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM ad_images WHERE ad_id=$1", id).Scan(&imgs))
	assert.Equal(t, 0, attrs, "значения атрибутов удалены каскадом")
	assert.Equal(t, 0, imgs, "изображения удалены каскадом")
}
