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

func eventOf(t *testing.T, subject, adID string) events.Envelope {
	t.Helper()
	e, err := events.New(subject, "listing", map[string]any{"ad_id": adID})
	require.NoError(t, err)
	return e
}

func createdEvent(t *testing.T, adID string) events.Envelope {
	t.Helper()
	return eventOf(t, events.SubjectAdCreated, adID)
}

// seedAd вставляет объявление с заданным статусом и сроком (через CreateWithEvent,
// который кладёт bozor.ad.created в outbox).
func seedAd(t *testing.T, ctx context.Context, r *repo.Repo, status domain.Status, expiresAt *time.Time) domain.Ad {
	t.Helper()
	id := newID(t)
	now := time.Now().UTC()
	ad := domain.Ad{
		ID: id, UserID: newID(t), CategoryID: newID(t), Title: "Объявление", Price: 1, Currency: "UZS",
		RegionID: 1, Status: status, ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, r.CreateWithEvent(ctx, ad, createdEvent(t, id)))
	return ad
}

// countSubject считает события указанного subject в outbox.
func countSubject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subject string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE subject = $1", subject).Scan(&n))
	return n
}

func TestListingRepo_TransitionWithEvent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	ad := seedAd(t, ctx, r, domain.StatusDraft, nil)

	// draft → pending применяется и публикует bozor.ad.updated.
	upd := domain.StatusUpdate{From: domain.StatusDraft, To: domain.StatusPending}
	require.NoError(t, r.TransitionWithEvent(ctx, ad.ID, upd, eventOf(t, events.SubjectAdUpdated, ad.ID)))

	got, err := r.GetByID(ctx, ad.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPending, got.Status)

	// Повтор из draft уже невозможен (статус pending) — ErrInvalidTransition, без события.
	err = r.TransitionWithEvent(ctx, ad.ID, upd, eventOf(t, events.SubjectAdUpdated, ad.ID))
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
	assert.Equal(t, 1, countSubject(t, ctx, pool, events.SubjectAdUpdated), "конфликтный переход не плодит событий")
}

func TestListingRepo_ApplyModeration_Idempotent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	ad := seedAd(t, ctx, r, domain.StatusPending, nil)
	now := time.Now().UTC()
	exp := now.Add(720 * time.Hour)
	upd := domain.StatusUpdate{From: domain.StatusPending, To: domain.StatusActive, PublishedAt: &now, ExpiresAt: &exp}

	const consumer = "listing-moderation"
	ev := eventOf(t, events.SubjectAdUpdated, ad.ID)
	require.NoError(t, r.ApplyModerationWithEvent(ctx, consumer, ev.ID, ad.ID, upd, ev))

	got, err := r.GetByID(ctx, ad.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, got.Status)
	require.NotNil(t, got.PublishedAt, "активация проставила published_at")
	require.NotNil(t, got.ExpiresAt, "активация проставила expires_at")

	done, err := r.IsEventProcessed(ctx, consumer, ev.ID)
	require.NoError(t, err)
	assert.True(t, done, "событие отмечено обработанным (inbox)")

	// Повторное применение того же решения: объявление уже active — ни перехода, ни события.
	require.NoError(t, r.ApplyModerationWithEvent(ctx, consumer, ev.ID, ad.ID, upd, eventOf(t, events.SubjectAdUpdated, ad.ID)))
	assert.Equal(t, 1, countSubject(t, ctx, pool, events.SubjectAdUpdated), "повтор не публикует событие второй раз")
}

func TestListingRepo_AddViews(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	a1 := seedAd(t, ctx, r, domain.StatusActive, nil)
	a2 := seedAd(t, ctx, r, domain.StatusActive, nil)

	// Batch-флеш: несуществующий id молча отбрасывается.
	require.NoError(t, r.AddViews(ctx, map[string]int64{a1.ID: 3, a2.ID: 5, newID(t): 9}))

	got1, err := r.GetByID(ctx, a1.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), got1.ViewsCount)
	got2, err := r.GetByID(ctx, a2.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(5), got2.ViewsCount)

	// Повторный флеш аккумулирует (views_count += delta).
	require.NoError(t, r.AddViews(ctx, map[string]int64{a1.ID: 2}))
	got1b, err := r.GetByID(ctx, a1.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(5), got1b.ViewsCount)

	// Пустая карта — no-op.
	require.NoError(t, r.AddViews(ctx, nil))
}

func TestListingRepo_ExpireFlow(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	expiredAd := seedAd(t, ctx, r, domain.StatusActive, &past)
	freshAd := seedAd(t, ctx, r, domain.StatusActive, &future) // срок не истёк
	seedAd(t, ctx, r, domain.StatusDraft, &past)               // не active — игнорируется

	list, err := r.ListExpired(ctx, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Len(t, list, 1, "только активное с истёкшим сроком")
	assert.Equal(t, expiredAd.ID, list[0].ID)

	ok, err := r.ExpireWithEvent(ctx, expiredAd.ID, eventOf(t, events.SubjectAdExpired, expiredAd.ID))
	require.NoError(t, err)
	assert.True(t, ok, "статус сменился на expired")

	got, err := r.GetByID(ctx, expiredAd.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusExpired, got.Status)

	// Повтор: уже expired — пропуск без события.
	ok2, err := r.ExpireWithEvent(ctx, expiredAd.ID, eventOf(t, events.SubjectAdExpired, expiredAd.ID))
	require.NoError(t, err)
	assert.False(t, ok2)

	gotFresh, err := r.GetByID(ctx, freshAd.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, gotFresh.Status, "объявление с будущим сроком не истекло")

	assert.Equal(t, 1, countSubject(t, ctx, pool, events.SubjectAdExpired), "ровно одно событие истечения")
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
