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

const serviceName = "user-profile"

// Deps — зависимости для сборки роутера User/Profile-сервиса.
type Deps struct {
	Log            *slog.Logger
	Handler        *Handler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер User/Profile-сервиса.
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

	r.Group(func(api chi.Router) {
		api.Use(authx.FromForwardedHeaders)

		// Свой профиль и настройки — только владелец (из идентичности).
		api.Get("/api/v1/me", d.Handler.Me)
		api.Patch("/api/v1/me", d.Handler.UpdateMe)
		api.Get("/api/v1/me/notification-prefs", d.Handler.GetPrefs)
		api.Put("/api/v1/me/notification-prefs", d.Handler.PutPrefs)
		// Публичный профиль продавца.
		api.Get("/api/v1/users/{id}", d.Handler.PublicProfile)
	})

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r
}
