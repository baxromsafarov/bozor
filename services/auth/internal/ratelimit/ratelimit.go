// Package ratelimit — лимитер фиксированного окна поверх Redis для защиты
// чувствительных публичных эндпоинтов Auth (инициация логина, вебхук).
// При недоступности Redis работает fail-open (запрос пропускается).
package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"
)

// ClientIPKey извлекает ключ лимитирования из клиентского IP: первый хоп
// X-Forwarded-For (от gateway/nginx), затем X-Real-Ip, иначе адрес соединения.
func ClientIPKey(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-Ip"); xr != "" {
		return strings.TrimSpace(xr)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// Limiter — счётчик фиксированного окна в Redis.
type Limiter struct {
	rdb redis.Cmdable
}

// New создаёт лимитер поверх клиента Redis.
func New(rdb redis.Cmdable) *Limiter {
	return &Limiter{rdb: rdb}
}

// Allow увеличивает счётчик key и сообщает, укладывается ли он в limit за window.
// retryAfter — время до сброса окна (имеет смысл при отказе).
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	pipe := l.rdb.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pttl := pipe.PTTL(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, fmt.Errorf("ratelimit: pipeline: %w", err)
	}

	count := incr.Val()
	ttl := pttl.Val()
	// Первый запрос в окне (или ключ без TTL) — устанавливаем окно.
	if count == 1 || ttl < 0 {
		if err := l.rdb.Expire(ctx, key, window).Err(); err != nil {
			return false, 0, fmt.Errorf("ratelimit: expire: %w", err)
		}
		ttl = window
	}
	if count > int64(limit) {
		return false, ttl, nil
	}
	return true, 0, nil
}

// Allower — проверка лимита (реализуется Limiter; в тестах — фейком).
type Allower interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)
}

// Middleware ограничивает частоту запросов по ключу name+keyFn(r). При отказе
// Redis пропускает запрос (fail-open), при превышении отдаёт 429 (RFC 7807)
// с заголовком Retry-After.
func Middleware(l Allower, name string, limit int, window time.Duration, keyFn func(*http.Request) string, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "auth:rl:" + name + ":" + keyFn(r)
			allowed, retry, err := l.Allow(r.Context(), key, limit, window)
			if err != nil {
				log.WarnContext(r.Context(), "rate-limit fail-open", slog.String("error", err.Error()))
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
				httpx.WriteProblem(w, r, apperr.New(apperr.KindRateLimited, "rate_limited",
					"Слишком много запросов, попробуйте позже", "So'rovlar juda ko'p, keyinroq urinib ko'ring"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
