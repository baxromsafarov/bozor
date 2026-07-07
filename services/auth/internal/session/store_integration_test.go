//go:build integration

package session_test

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"bozor/services/auth/internal/session"
)

// newStore поднимает реальный Redis и возвращает хранилище сессий над ним.
func newStore(t *testing.T) *session.Store {
	t.Helper()
	ctx := context.Background()

	rc, err := tcredis.Run(ctx, "redis:8-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(rc) })

	uri, err := rc.ConnectionString(ctx)
	require.NoError(t, err)
	opts, err := redis.ParseURL(uri)
	require.NoError(t, err)

	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Ping(ctx).Err())

	return session.NewStore(client)
}

// TestSessionFlow_Integration проверяет полный жизненный цикл nonce на реальном
// Redis: init → link → confirm → get(confirmed).
func TestSessionFlow_Integration(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	const tgUserID = int64(42)
	const userID = "00000000-0000-7000-8000-000000000001"

	nonce, ttl, err := store.Init(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, nonce)
	assert.Equal(t, session.TTL, ttl)

	// Сразу после init — pending.
	sess, err := store.Get(ctx, nonce)
	require.NoError(t, err)
	assert.Equal(t, session.StatusPending, sess.Status)

	// Пользователь открыл deep-link (Link) и поделился контактом (Confirm).
	require.NoError(t, store.Link(ctx, nonce, tgUserID))
	confirmedNonce, err := store.Confirm(ctx, tgUserID, userID)
	require.NoError(t, err)
	assert.Equal(t, nonce, confirmedNonce)

	// Теперь опрос отдаёт confirmed с user_id.
	sess, err = store.Get(ctx, nonce)
	require.NoError(t, err)
	assert.Equal(t, session.StatusConfirmed, sess.Status)
	assert.Equal(t, userID, sess.UserID)
}

// TestSession_UnknownNonceExpired: неизвестный nonce трактуется как expired.
func TestSession_UnknownNonceExpired(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	sess, err := store.Get(ctx, "deadbeefdeadbeefdeadbeefdeadbeef")
	require.NoError(t, err)
	assert.Equal(t, session.StatusExpired, sess.Status)
}

// TestSession_SingleUse: подтверждённый nonce нельзя связать повторно.
func TestSession_SingleUse(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	nonce, _, err := store.Init(ctx)
	require.NoError(t, err)
	require.NoError(t, store.Link(ctx, nonce, 1))
	_, err = store.Confirm(ctx, 1, "user-1")
	require.NoError(t, err)

	// Повторная привязка того же nonce (другим пользователем) отклоняется.
	err = store.Link(ctx, nonce, 2)
	assert.ErrorIs(t, err, session.ErrNotPending)
}

// TestSession_ConfirmWithoutLink: контакт без предшествующего deep-link не
// подтверждает никакой nonce (пустой результат, без ошибки).
func TestSession_ConfirmWithoutLink(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	nonce, err := store.Confirm(ctx, 777, "user-777")
	require.NoError(t, err)
	assert.Empty(t, nonce)
}

// TestSession_LinkUnknownNonce: связать несуществующий nonce нельзя.
func TestSession_LinkUnknownNonce(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	err := store.Link(ctx, "ffffffffffffffffffffffffffffffff", 5)
	assert.ErrorIs(t, err, session.ErrNotFound)
}
