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

	"bozor/services/user-profile/internal/domain"
	"bozor/services/user-profile/internal/repo"
	"bozor/services/user-profile/migrations"
)

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_profile"),
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

func updatedEvent(t *testing.T, userID string) events.Envelope {
	t.Helper()
	e, err := events.New(events.SubjectUserUpdated, "user-profile", map[string]any{"user_id": userID})
	require.NoError(t, err)
	return e
}

func TestRepo_EnsureAndGetProfile(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)

	// Профиля ещё нет.
	_, err := r.GetProfile(ctx, uid)
	require.ErrorIs(t, err, domain.ErrProfileNotFound)

	p := domain.NewDefaultProfile(uid, "uz", time.Now().UTC())
	require.NoError(t, r.EnsureProfile(ctx, p))

	got, err := r.GetProfile(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, uid, got.UserID)
	assert.Equal(t, domain.UserTypeIndividual, got.UserType)
	assert.Equal(t, "uz", got.LanguageCode)
	assert.True(t, got.ContactPhoneVisible)

	// Повторный EnsureProfile идемпотентен (не перезаписывает).
	p2 := domain.NewDefaultProfile(uid, "ru", time.Now().UTC())
	require.NoError(t, r.EnsureProfile(ctx, p2))
	got2, err := r.GetProfile(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, "uz", got2.LanguageCode, "существующий профиль не перезаписан")
}

func TestRepo_CreateProfileWithInbox_Idempotent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	uid, eventID := newID(t), newID(t)
	const consumer = "user-profile-users"

	p := domain.NewDefaultProfile(uid, "ru", time.Now().UTC())
	require.NoError(t, r.CreateProfileWithInbox(ctx, p, consumer, eventID))

	processed, err := r.IsEventProcessed(ctx, consumer, eventID)
	require.NoError(t, err)
	assert.True(t, processed, "событие отмечено обработанным")

	// Повтор той же операции не создаёт дубля (ON CONFLICT DO NOTHING + inbox).
	require.NoError(t, r.CreateProfileWithInbox(ctx, p, consumer, eventID))
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM profiles WHERE user_id=$1`, uid).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestRepo_UpdateProfileWithEvent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	uid := newID(t)

	// Обновление отсутствующего профиля → ErrProfileNotFound.
	p := domain.NewDefaultProfile(uid, "ru", time.Now().UTC())
	require.ErrorIs(t, r.UpdateProfileWithEvent(ctx, p, updatedEvent(t, uid)), domain.ErrProfileNotFound)

	require.NoError(t, r.EnsureProfile(ctx, p))

	p.DisplayName = "Азиз"
	p.UserType = domain.UserTypeBusiness
	p.BusinessName = "TechShop"
	city := int64(14)
	p.CityID = &city
	require.NoError(t, r.UpdateProfileWithEvent(ctx, p, updatedEvent(t, uid)))

	got, err := r.GetProfile(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, "Азиз", got.DisplayName)
	assert.Equal(t, domain.UserTypeBusiness, got.UserType)
	require.NotNil(t, got.CityID)
	assert.Equal(t, int64(14), *got.CityID)

	// Событие bozor.user.updated лежит в outbox (одной транзакцией с UPDATE).
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE subject = $1`, events.SubjectUserUpdated).Scan(&n))
	assert.Equal(t, 1, n, "одно событие в outbox")
}

func TestRepo_GetRating_ZeroWhenAbsent(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)
	require.NoError(t, r.EnsureProfile(ctx, domain.NewDefaultProfile(uid, "ru", time.Now().UTC())))

	rt, err := r.GetRating(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, 0.0, rt.AvgRating)
	assert.Equal(t, 0, rt.ReviewsCount)
}

func TestRepo_ReplaceNotificationPrefs(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)
	require.NoError(t, r.EnsureProfile(ctx, domain.NewDefaultProfile(uid, "ru", time.Now().UTC())))

	require.NoError(t, r.ReplaceNotificationPrefs(ctx, uid, []domain.NotificationPref{
		{Channel: domain.ChannelTelegram, EventType: domain.NotifyAdStatus, Enabled: false},
		{Channel: domain.ChannelTelegram, EventType: domain.NotifyChatMessage, Enabled: true},
	}))
	prefs, err := r.GetNotificationPrefs(ctx, uid)
	require.NoError(t, err)
	assert.Len(t, prefs, 2)

	// Замена меньшим набором вытесняет прежние строки.
	require.NoError(t, r.ReplaceNotificationPrefs(ctx, uid, []domain.NotificationPref{
		{Channel: domain.ChannelTelegram, EventType: domain.NotifyReview, Enabled: false},
	}))
	prefs, err = r.GetNotificationPrefs(ctx, uid)
	require.NoError(t, err)
	require.Len(t, prefs, 1)
	assert.Equal(t, domain.NotifyReview, prefs[0].EventType)
}
