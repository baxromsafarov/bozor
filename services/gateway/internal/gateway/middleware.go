package gateway

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/gateway/internal/ratelimit"
)

// Заголовки идентичности, проставляемые gateway после проверки JWT.
// Внутренние сервисы доверяют им (см. authx.FromForwardedHeaders).
const (
	headerUserID    = "X-User-Id"
	headerUserRoles = "X-User-Roles"
)

// bearerPrefix — схема в заголовке Authorization.
const bearerPrefix = "Bearer "

// identityHeaders — заголовки, которым нельзя доверять от клиента.
var identityHeaders = []string{headerUserID, headerUserRoles}

// Заголовки лимитирования (RFC-подобные X-RateLimit-*).
const (
	headerRateLimit     = "X-RateLimit-Limit"
	headerRateRemaining = "X-RateLimit-Remaining"
	headerRateReset     = "X-RateLimit-Reset"
	headerRetryAfter    = "Retry-After"
)

// StripIdentityHeaders удаляет из входящего запроса заголовки идентичности:
// проставлять их вправе только gateway, клиентские значения — попытка подделки.
func StripIdentityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range identityHeaders {
			r.Header.Del(h)
		}
		next.ServeHTTP(w, r)
	})
}

// OptionalAuth проверяет access-JWT, если он передан в Authorization.
// Отсутствие токена — не ошибка: требование авторизации для конкретных
// эндпоинтов обеспечивают сами сервисы. Недействительный токен → 401
// RFC 7807. При валидном токене в запрос проставляются заголовки
// идентичности для проброса во внутренний сервис.
func OptionalAuth(key []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if authz == "" {
				next.ServeHTTP(w, r)
				return
			}
			if len(authz) <= len(bearerPrefix) || !strings.EqualFold(authz[:len(bearerPrefix)], bearerPrefix) {
				httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "invalid_token",
					"Недействительный токен", "Yaroqsiz token"))
				return
			}
			claims, err := authx.Parse(key, strings.TrimSpace(authz[len(bearerPrefix):]))
			if err != nil {
				httpx.WriteProblem(w, r, err)
				return
			}
			r.Header.Set(headerUserID, claims.Subject)
			if len(claims.Roles) > 0 {
				r.Header.Set(headerUserRoles, strings.Join(claims.Roles, ","))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit ограничивает частоту запросов через token bucket. Ключ —
// пользователь (если запрос аутентифицирован) либо IP клиента. Лимит
// сопровождается заголовками X-RateLimit-*; при исчерпании — 429 RFC 7807.
// Если лимитер недоступен, запрос пропускается (fail-open) с логом WARN:
// защита не должна ронять шлюз.
func RateLimit(l ratelimit.Limiter, rate float64, burst int, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res, err := l.Allow(r.Context(), rateLimitKey(r), rate, burst)
			if err != nil {
				log.WarnContext(r.Context(), "rate limiter недоступен, запрос пропущен",
					slog.String("error", err.Error()))
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set(headerRateLimit, strconv.Itoa(burst))
			w.Header().Set(headerRateRemaining, strconv.Itoa(res.Remaining))
			w.Header().Set(headerRateReset, strconv.Itoa(res.ResetSec))

			if !res.Allowed {
				w.Header().Set(headerRetryAfter, strconv.Itoa(res.ResetSec))
				httpx.WriteProblem(w, r, apperr.New(apperr.KindRateLimited, "rate_limited",
					"Слишком много запросов", "So'rovlar juda ko'p"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimitKey строит ключ лимита: по пользователю, если запрос
// аутентифицирован (OptionalAuth проставил заголовок), иначе по IP.
func rateLimitKey(r *http.Request) string {
	if id := r.Header.Get(headerUserID); id != "" {
		return "rl:user:" + id
	}
	return "rl:ip:" + clientIP(r)
}

// clientIP определяет IP клиента: первый адрес из X-Forwarded-For
// (его проставляет edge-nginx), иначе — адрес соединения.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// CORS проставляет заголовки CORS и обрабатывает preflight (OPTIONS).
// allowed — список разрешённых источников; "*" разрешает любой.
func CORS(allowed []string) func(http.Handler) http.Handler {
	allowAll := false
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		if o == "*" {
			allowAll = true
		}
		set[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); origin != "" {
				switch {
				case allowAll:
					w.Header().Set("Access-Control-Allow-Origin", "*")
				default:
					if _, ok := set[origin]; ok {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Add("Vary", "Origin")
					}
				}
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h := w.Header()
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers",
					"Authorization, Content-Type, Idempotency-Key, X-Request-Id, Accept-Language")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
