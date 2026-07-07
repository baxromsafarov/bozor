package ratelimit_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/gateway/internal/ratelimit"
)

// TestRedisLimiter — интеграционный тест token-bucket против реального Redis.
// Запускается, только если задан REDIS_TEST_ADDR (например, при поднятом
// docker compose или в CI с сервисом redis); иначе пропускается.
func TestRedisLimiter(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR не задан — пропуск интеграционного теста")
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err(), "redis должен быть доступен")

	limiter := ratelimit.NewRedisLimiter(client)
	// Уникальный ключ на прогон, чтобы тесты не влияли друг на друга.
	key := fmt.Sprintf("test:rl:%d", time.Now().UnixNano())
	const rate, burst = 1.0, 3

	t.Run("burst разрешает ровно burst запросов", func(t *testing.T) {
		for i := 0; i < burst; i++ {
			res, err := limiter.Allow(ctx, key, rate, burst)
			require.NoError(t, err)
			assert.True(t, res.Allowed, "запрос %d в пределах burst должен пройти", i+1)
		}
		res, err := limiter.Allow(ctx, key, rate, burst)
		require.NoError(t, err)
		assert.False(t, res.Allowed, "запрос сверх burst должен быть отклонён")
		assert.Positive(t, res.ResetSec, "при отказе указывается время до пополнения")
	})
}
