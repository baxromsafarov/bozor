//go:build integration

package views_test

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"bozor/services/listing/internal/views"
)

func newCounter(t *testing.T) *views.Counter {
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

	return views.NewCounter(client)
}

func TestCounter_IncrBufferedDrain(t *testing.T) {
	ctx := context.Background()
	c := newCounter(t)

	require.NoError(t, c.Incr(ctx, "ad-1"))
	require.NoError(t, c.Incr(ctx, "ad-1"))
	require.NoError(t, c.Incr(ctx, "ad-2"))

	n, err := c.Buffered(ctx, "ad-1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	// Отсутствующее объявление — 0, без ошибки.
	n0, err := c.Buffered(ctx, "missing")
	require.NoError(t, err)
	assert.Equal(t, int64(0), n0)

	// Drain снимает и обнуляет весь буфер.
	counts, err := c.Drain(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"ad-1": 2, "ad-2": 1}, counts)

	after, err := c.Buffered(ctx, "ad-1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), after, "после снятия буфер пуст")

	// Повторный Drain пустого буфера — пусто, без ошибки.
	empty, err := c.Drain(ctx)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestCounter_Restore(t *testing.T) {
	ctx := context.Background()
	c := newCounter(t)

	require.NoError(t, c.Restore(ctx, map[string]int64{"ad-1": 5}))
	n, err := c.Buffered(ctx, "ad-1")
	require.NoError(t, err)
	assert.Equal(t, int64(5), n, "возврат в буфер восстанавливает счётчик")

	// Инкремент после возврата продолжает копить.
	require.NoError(t, c.Incr(ctx, "ad-1"))
	n2, err := c.Buffered(ctx, "ad-1")
	require.NoError(t, err)
	assert.Equal(t, int64(6), n2)
}

func TestCounter_DrainPreservesConcurrentIncrements(t *testing.T) {
	ctx := context.Background()
	c := newCounter(t)

	require.NoError(t, c.Incr(ctx, "ad-1"))
	counts, err := c.Drain(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"ad-1": 1}, counts)

	// Инкремент после снятия попадает в свежий ключ и не теряется.
	require.NoError(t, c.Incr(ctx, "ad-1"))
	n, err := c.Buffered(ctx, "ad-1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}
