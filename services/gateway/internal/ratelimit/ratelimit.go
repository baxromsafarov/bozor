// Package ratelimit реализует ограничение частоты запросов алгоритмом
// token bucket поверх Redis (атомарно, через Lua-скрипт).
package ratelimit

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// script — Lua token-bucket, встраивается в бинарь на этапе сборки.
//
//go:embed token_bucket.lua
var script string

// Result — исход проверки лимита.
type Result struct {
	Allowed   bool // разрешён ли запрос
	Remaining int  // сколько токенов осталось в bucket'е
	ResetSec  int  // через сколько секунд появится токен (при Allowed=false)
}

// Limiter проверяет лимит для ключа. Реализации: RedisLimiter (прод),
// фейки в тестах.
type Limiter interface {
	// Allow списывает один токен из bucket'а key ёмкостью burst,
	// пополняемого со скоростью rate токенов/сек.
	Allow(ctx context.Context, key string, rate float64, burst int) (Result, error)
}

// RedisLimiter — token bucket поверх Redis.
type RedisLimiter struct {
	client *redis.Client
	script *redis.Script
}

// NewRedisLimiter создаёт лимитер, использующий клиент client.
func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{client: client, script: redis.NewScript(script)}
}

// Allow выполняет атомарную проверку и списание токена.
func (l *RedisLimiter) Allow(ctx context.Context, key string, rate float64, burst int) (Result, error) {
	raw, err := l.script.Run(ctx, l.client, []string{key}, rate, burst, 1).Result()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: выполнение скрипта: %w", err)
	}
	vals, ok := raw.([]any)
	if !ok || len(vals) != 3 {
		return Result{}, fmt.Errorf("ratelimit: неожиданный ответ redis: %v", raw)
	}
	allowed, _ := vals[0].(int64)
	remaining, _ := vals[1].(int64)
	resetMS, _ := vals[2].(int64)
	return Result{
		Allowed:   allowed == 1,
		Remaining: int(remaining),
		ResetSec:  int((resetMS + 999) / 1000), // округление вверх до секунд
	}, nil
}
