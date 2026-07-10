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

const serviceName = "reviews"

// Deps — зависимости для сборки роутера Reviews-сервиса.
type Deps struct {
	Log            *slog.Logger
	Reviews        *ReviewHandler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Reviews-сервиса.
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

	if d.Reviews != nil {
		// Внутренний эндпоинт (только сеть compose, без авторизации): агрегат
		// рейтинга продавца для кеша Profile (9.2), как /internal/ads Listing.
		r.Get("/internal/users/{userID}/rating", d.Reviews.Rating)

		r.Group(func(api chi.Router) {
			api.Use(authx.FromForwardedHeaders)

			// Публично: лента отзывов о пользователе.
			api.Get("/api/v1/users/{userID}/reviews", d.Reviews.ListByUser)

			// Создание отзыва — только аутентифицированный автор.
			api.With(requireAuth).Post("/api/v1/reviews", d.Reviews.Create)
		})
	}

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r
}

// requireAuth пропускает только аутентифицированного пользователя (аноним → 401).
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authx.UserID(r.Context()) == "" {
			httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
				"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
