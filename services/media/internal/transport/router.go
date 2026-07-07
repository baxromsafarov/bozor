// Package transport собирает HTTP-слой Media-сервиса. Загрузка требует
// аутентификации (владелец из forwarded-идентичности gateway); чтение
// метаданных публично.
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

const serviceName = "media"

// Deps — зависимости для сборки роутера Media-сервиса.
type Deps struct {
	Log            *slog.Logger
	Handler        *Handler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Media-сервиса.
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

		// Загрузка — только аутентифицированный пользователь (владелец).
		api.Post("/api/v1/media", d.Handler.Upload)
		// Метаданные — публично.
		api.Get("/api/v1/media/{id}", d.Handler.Get)
	})

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r
}
