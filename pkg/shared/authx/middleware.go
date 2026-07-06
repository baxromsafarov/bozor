package authx

import (
	"net/http"
	"strings"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"
)

// bearerPrefix — схема авторизации в заголовке Authorization.
const bearerPrefix = "Bearer "

// Auth возвращает middleware, проверяющее JWT из заголовка
// "Authorization: Bearer <token>". При отсутствии токена возвращается
// 401 с кодом "missing_token", при недействительном токене — 401
// с кодом "invalid_token" (оба — RFC 7807 через httpx.WriteProblem).
// При валидном токене в контекст запроса кладутся userID (claims.Subject),
// роли и jti.
func Auth(key []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if len(authz) <= len(bearerPrefix) || !strings.EqualFold(authz[:len(bearerPrefix)], bearerPrefix) {
				httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "missing_token",
					"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
				return
			}

			claims, err := Parse(key, strings.TrimSpace(authz[len(bearerPrefix):]))
			if err != nil {
				httpx.WriteProblem(w, r, err)
				return
			}

			ctx := withAuth(r.Context(), claims.Subject, claims.Roles, claims.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole возвращает middleware, требующее наличия роли role
// у пользователя из контекста. При отсутствии роли возвращается 403
// с кодом "forbidden" (RFC 7807 через httpx.WriteProblem).
// Middleware ставится после Auth или FromForwardedHeaders.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !HasRole(r.Context(), role) {
				httpx.WriteProblem(w, r, apperr.New(apperr.KindForbidden, "forbidden",
					"Доступ запрещён", "Ruxsat berilmagan"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// FromForwardedHeaders — middleware для внутренних сервисов за gateway:
// читает заголовки X-User-Id и X-User-Roles (роли через запятую),
// проставленные gateway после проверки JWT, и кладёт их в контекст запроса.
// Проверка подписи не выполняется — заголовкам доверяет только внутренняя сеть.
func FromForwardedHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-Id")

		var roles []string
		for _, part := range strings.Split(r.Header.Get("X-User-Roles"), ",") {
			if role := strings.TrimSpace(part); role != "" {
				roles = append(roles, role)
			}
		}

		if userID != "" || len(roles) > 0 {
			r = r.WithContext(withAuth(r.Context(), userID, roles, ""))
		}
		next.ServeHTTP(w, r)
	})
}
