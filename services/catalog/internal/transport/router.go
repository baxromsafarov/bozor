// Package transport собирает HTTP-слой Catalog-сервиса. Публичные маршруты идут
// под /api/v1/categories/* — так их проксирует gateway. Запись доступна только
// персоналу (роли admin/moderator из forwarded-идентичности gateway).
package transport

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/otelx"
)

const serviceName = "catalog"

// Deps — зависимости для сборки роутера Catalog-сервиса.
type Deps struct {
	Log            *slog.Logger
	Handler        *Handler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Catalog-сервиса.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.Recover(d.Log))
	r.Use(httpx.RequestID)
	r.Use(httpx.Lang)
	r.Use(httpx.AccessLog(d.Log))
	r.Use(otelx.HTTPMiddleware(serviceName))

	r.Get("/healthz", httpx.HealthHandler())
	r.Get("/readyz", httpx.ReadyHandler(d.ReadyChecks))
	if d.MetricsHandler != nil {
		r.Handle("/metrics", d.MetricsHandler)
	}

	// Идентичность из forwarded-заголовков gateway доступна всем /api/v1-роутам.
	r.Group(func(api chi.Router) {
		api.Use(authx.FromForwardedHeaders)

		// Чтение дерева — публично (плоский путь без завершающего слеша).
		api.Get("/api/v1/categories", d.Handler.Tree)

		// Запись — только персонал (admin/moderator).
		api.With(requireStaff).Post("/api/v1/categories", d.Handler.Create)
		api.With(requireStaff).Patch("/api/v1/categories/{id}", d.Handler.Update)
		api.With(requireStaff).Delete("/api/v1/categories/{id}", d.Handler.Delete)
	})

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r
}

// requireStaff пропускает только аутентифицированный персонал (admin/moderator).
// Аноним → 401, недостаточно прав → 403.
func requireStaff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authx.UserID(r.Context()) == "" {
			httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
				"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
			return
		}
		if !authx.HasRole(r.Context(), "admin") && !authx.HasRole(r.Context(), "moderator") {
			httpx.WriteProblem(w, r, apperr.New(apperr.KindForbidden, "forbidden",
				"Доступ запрещён", "Ruxsat berilmagan"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
