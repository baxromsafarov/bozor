// Package ratelimit — потокобезопасный per-user token-bucket лимит отправки
// сообщений чата (in-process). При нескольких репликах лимит действует на
// реплику; для чата этого достаточно: клиент обычно держит одно WS-соединение к
// одной реплике (nginx sticky), а глобальную защиту даёт rate-limit на gateway.
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// bucket — лимитер пользователя и момент последнего обращения (для очистки).
type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// UserLimiter выдаёт по token-bucket на пользователя; неактивные вычищаются.
type UserLimiter struct {
	mu    sync.Mutex
	users map[string]*bucket
	limit rate.Limit
	burst int
}

// New создаёт лимитер (perSec токенов/сек, burst — всплеск) и запускает фоновую
// очистку неактивных пользователей до отмены ctx.
func New(ctx context.Context, perSec float64, burst int) *UserLimiter {
	u := &UserLimiter{
		users: make(map[string]*bucket),
		limit: rate.Limit(perSec),
		burst: burst,
	}
	go u.janitor(ctx)
	return u
}

// Allow сообщает, разрешена ли отправка пользователю сейчас (списывает токен).
func (u *UserLimiter) Allow(userID string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	b := u.users[userID]
	if b == nil {
		b = &bucket{lim: rate.NewLimiter(u.limit, u.burst)}
		u.users[userID] = b
	}
	b.seen = time.Now()
	return b.lim.Allow()
}

// janitor периодически удаляет пользователей без активности > 10 минут.
func (u *UserLimiter) janitor(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cutoff := time.Now().Add(-10 * time.Minute)
			u.mu.Lock()
			for id, b := range u.users {
				if b.seen.Before(cutoff) {
					delete(u.users, id)
				}
			}
			u.mu.Unlock()
		}
	}
}
