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

	"bozor/services/auth/internal/domain"
	"bozor/services/auth/internal/repo"
	"bozor/services/auth/migrations"
)

// startAuthDB поднимает postgres с применёнными миграциями Auth.
func startAuthDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	return pool
}

// insertUser вставляет пользователя (нужен из-за FK refresh_tokens.user_id).
func insertUser(t *testing.T, pool *pgxpool.Pool, tgID int64) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(),
		`INSERT INTO users (id, telegram_user_id, phone) VALUES ($1, $2, $3)`,
		id.String(), tgID, "+998901112233")
	require.NoError(t, err)
	return id.String()
}

// testDevice — device_id, используемый во всех интеграционных сценариях.
const testDevice = "dev-1"

// insertRefresh вставляет refresh-токен (устройство testDevice) и возвращает его хеш.
func insertRefresh(t *testing.T, r *repo.RefreshRepo, userID, family string, expires time.Time) []byte {
	t.Helper()
	_, hash, err := domain.NewRefreshToken()
	require.NoError(t, err)
	id, err := uuid.NewV7()
	require.NoError(t, err)
	require.NoError(t, r.Insert(context.Background(), domain.RefreshInsert{
		ID: id.String(), UserID: userID, TokenHash: hash,
		DeviceID: testDevice, FamilyID: family, ExpiresAt: expires,
	}))
	return hash
}

func isRevoked(t *testing.T, pool *pgxpool.Pool, hash []byte) bool {
	t.Helper()
	var revoked *time.Time
	err := pool.QueryRow(context.Background(),
		"SELECT revoked_at FROM refresh_tokens WHERE token_hash=$1", hash).Scan(&revoked)
	require.NoError(t, err)
	return revoked != nil
}

func newFamily(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}

func TestRefreshRotate_HappyPath(t *testing.T) {
	ctx := context.Background()
	pool := startAuthDB(t)
	r := repo.NewRefreshRepo(pool)
	userID := insertUser(t, pool, 1001)
	family := newFamily(t)

	hash1 := insertRefresh(t, r, userID, family, time.Now().Add(time.Hour))

	_, newHash, _ := domain.NewRefreshToken()
	newID, _ := uuid.NewV7()
	res, err := r.Rotate(ctx, hash1, testDevice, newID.String(), newHash, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, userID, res.UserID)
	assert.Equal(t, family, res.FamilyID)
	assert.Equal(t, testDevice, res.DeviceID)

	assert.True(t, isRevoked(t, pool, hash1), "старый токен погашен")
	assert.False(t, isRevoked(t, pool, newHash), "новый токен активен")
}

func TestRefreshRotate_ReuseRevokesFamily(t *testing.T) {
	ctx := context.Background()
	pool := startAuthDB(t)
	r := repo.NewRefreshRepo(pool)
	userID := insertUser(t, pool, 1002)
	family := newFamily(t)

	hash1 := insertRefresh(t, r, userID, family, time.Now().Add(time.Hour))

	// Легитимная ротация: hash1 → hash2.
	_, hash2, _ := domain.NewRefreshToken()
	id2, _ := uuid.NewV7()
	_, err := r.Rotate(ctx, hash1, testDevice, id2.String(), hash2, time.Now().Add(time.Hour))
	require.NoError(t, err)

	// Повторное предъявление уже погашенного hash1 → reuse → отзыв семейства.
	_, hash3, _ := domain.NewRefreshToken()
	id3, _ := uuid.NewV7()
	_, err = r.Rotate(ctx, hash1, testDevice, id3.String(), hash3, time.Now().Add(time.Hour))
	require.ErrorIs(t, err, domain.ErrTokenReuse)

	assert.True(t, isRevoked(t, pool, hash2), "reuse отзывает и активный токен семейства")

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM auth_audit_log WHERE event='refresh_reuse_detected'").Scan(&n))
	assert.Equal(t, 1, n, "reuse-detection записан в аудит")
}

func TestRefreshRotate_DeviceMismatch(t *testing.T) {
	ctx := context.Background()
	pool := startAuthDB(t)
	r := repo.NewRefreshRepo(pool)
	userID := insertUser(t, pool, 1003)
	hash := insertRefresh(t, r, userID, newFamily(t), time.Now().Add(time.Hour))

	_, newHash, _ := domain.NewRefreshToken()
	id, _ := uuid.NewV7()
	_, err := r.Rotate(ctx, hash, "dev-OTHER", id.String(), newHash, time.Now().Add(time.Hour))
	assert.ErrorIs(t, err, domain.ErrDeviceMismatch)
}

func TestRefreshRotate_NotFound(t *testing.T) {
	ctx := context.Background()
	pool := startAuthDB(t)
	r := repo.NewRefreshRepo(pool)

	_, unknown, _ := domain.NewRefreshToken()
	_, newHash, _ := domain.NewRefreshToken()
	id, _ := uuid.NewV7()
	_, err := r.Rotate(ctx, unknown, testDevice, id.String(), newHash, time.Now().Add(time.Hour))
	assert.ErrorIs(t, err, domain.ErrTokenNotFound)
}

func TestRefreshRotate_Expired(t *testing.T) {
	ctx := context.Background()
	pool := startAuthDB(t)
	r := repo.NewRefreshRepo(pool)
	userID := insertUser(t, pool, 1004)
	hash := insertRefresh(t, r, userID, newFamily(t), time.Now().Add(-time.Minute))

	_, newHash, _ := domain.NewRefreshToken()
	id, _ := uuid.NewV7()
	_, err := r.Rotate(ctx, hash, testDevice, id.String(), newHash, time.Now().Add(time.Hour))
	assert.ErrorIs(t, err, domain.ErrTokenExpired)
	assert.True(t, isRevoked(t, pool, hash), "истёкший токен помечается погашенным")
}

func TestRefreshRevokeFamily_Logout(t *testing.T) {
	ctx := context.Background()
	pool := startAuthDB(t)
	r := repo.NewRefreshRepo(pool)
	userID := insertUser(t, pool, 1005)
	family := newFamily(t)

	// Два активных токена в одном семействе (напр. после ротации).
	hash1 := insertRefresh(t, r, userID, family, time.Now().Add(time.Hour))
	hash2 := insertRefresh(t, r, userID, family, time.Now().Add(time.Hour))

	uid, found, err := r.RevokeFamily(ctx, hash1)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, userID, uid)
	assert.True(t, isRevoked(t, pool, hash1))
	assert.True(t, isRevoked(t, pool, hash2), "logout отзывает всё семейство")

	// Идемпотентность и неизвестный токен.
	_, found2, err := r.RevokeFamily(ctx, hash1)
	require.NoError(t, err)
	assert.True(t, found2, "повторный logout — не ошибка")

	_, foundUnknown, err := r.RevokeFamily(ctx, domain.HashRefreshToken("nope"))
	require.NoError(t, err)
	assert.False(t, foundUnknown, "неизвестный токен → found=false")
}
