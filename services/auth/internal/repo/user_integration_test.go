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

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/migrate"

	"bozor/services/auth/internal/domain"
	"bozor/services/auth/internal/repo"
	"bozor/services/auth/migrations"
)

// TestUpsertUserWithEvent_Integration поднимает реальный PostgreSQL, применяет
// миграции сервиса и проверяет: первый апсерт создаёт пользователя и кладёт
// событие в outbox; повторный апсерт обновляет (created=false, без нового события).
func TestUpsertUserWithEvent_Integration(t *testing.T) {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_auth"),
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

	r := repo.NewUserRepo(pool)
	u := domain.User{
		ID: "00000000-0000-7000-8000-000000000001", TelegramUserID: 42,
		Phone: "+998901234567", FirstName: "Али", LanguageCode: "uz",
	}
	ev, err := events.New(events.SubjectUserCreated, "auth", map[string]any{"user_id": u.ID})
	require.NoError(t, err)

	// Первый апсерт — создание + событие в outbox.
	id, created, err := r.UpsertUserWithEvent(ctx, u, ev)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, u.ID, id, "при создании возвращается сгенерированный id")

	var users, outbox int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE subject=$1", events.SubjectUserCreated).Scan(&outbox))
	assert.Equal(t, 1, users)
	assert.Equal(t, 1, outbox, "при создании событие кладётся в outbox")

	// Повторный апсерт того же telegram_user_id — обновление, без нового события.
	u.Phone = "+998900000000"
	ev2, err := events.New(events.SubjectUserCreated, "auth", map[string]any{"user_id": u.ID})
	require.NoError(t, err)
	id2, created, err := r.UpsertUserWithEvent(ctx, u, ev2)
	require.NoError(t, err)
	assert.False(t, created, "существующий пользователь только обновляется")
	assert.Equal(t, u.ID, id2, "апсерт возвращает существующий id пользователя")

	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM outbox").Scan(&outbox))
	assert.Equal(t, 1, users, "новый пользователь не создан")
	assert.Equal(t, 1, outbox, "повторный апсерт не плодит события")

	var phone string
	require.NoError(t, pool.QueryRow(ctx, "SELECT phone FROM users WHERE telegram_user_id=42").Scan(&phone))
	assert.Equal(t, "+998900000000", phone, "телефон обновлён")
}

func TestUserRepo_BanUser(t *testing.T) {
	ctx := context.Background()
	pool := startAuthDB(t)
	r := repo.NewUserRepo(pool)
	refreshRepo := repo.NewRefreshRepo(pool)

	userID := insertUser(t, pool, 4242)
	// Два активных refresh-токена пользователя.
	insertRefresh(t, refreshRepo, userID, newFamily(t), time.Now().Add(time.Hour))
	insertRefresh(t, refreshRepo, userID, newFamily(t), time.Now().Add(time.Hour))

	revoked, err := r.BanUser(ctx, userID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, revoked, "оба токена отозваны")

	var status string
	require.NoError(t, pool.QueryRow(ctx, "SELECT status FROM users WHERE id=$1", userID).Scan(&status))
	assert.Equal(t, "banned", status)

	var active int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM refresh_tokens WHERE user_id=$1 AND revoked_at IS NULL", userID).Scan(&active))
	assert.Equal(t, 0, active, "активных токенов не осталось")

	// Идемпотентность: повторный бан не находит активных токенов.
	revoked, err = r.BanUser(ctx, userID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, revoked)
}
