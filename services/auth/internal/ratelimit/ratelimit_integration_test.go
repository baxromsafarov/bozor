//go:build integration

package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"bozor/services/auth/internal/ratelimit"
)

func newLimiter(t *testing.T) *ratelimit.Limiter {
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

	return ratelimit.New(client)
}

// TestAllow_FixedWindow: первые limit запросов проходят, следующий отклоняется
// с ненулевым retryAfter.
func TestAllow_FixedWindow(t *testing.T) {
	ctx := context.Background()
	l := newLimiter(t)
	const limit = 3
	const key = "test:rl:fixed"

	for i := 0; i < limit; i++ {
		allowed, _, err := l.Allow(ctx, key, limit, time.Minute)
		require.NoError(t, err)
		assert.True(t, allowed, "запрос %d в пределах лимита", i+1)
	}

	allowed, retry, err := l.Allow(ctx, key, limit, time.Minute)
	require.NoError(t, err)
	assert.False(t, allowed, "запрос сверх лимита отклонён")
	assert.Positive(t, retry, "указано время до сброса окна")
}

// TestAllow_WindowResets: после истечения окна счётчик обнуляется.
func TestAllow_WindowResets(t *testing.T) {
	ctx := context.Background()
	l := newLimiter(t)
	const key = "test:rl:reset"

	allowed, _, err := l.Allow(ctx, key, 1, time.Second)
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, _, err = l.Allow(ctx, key, 1, time.Second)
	require.NoError(t, err)
	assert.False(t, allowed, "второй запрос в том же окне отклонён")

	time.Sleep(1100 * time.Millisecond)
	allowed, _, err = l.Allow(ctx, key, 1, time.Second)
	require.NoError(t, err)
	assert.True(t, allowed, "после сброса окна запрос снова проходит")
}
