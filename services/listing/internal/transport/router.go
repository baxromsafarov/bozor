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

const serviceName = "listing"

// Deps — зависимости для сборки роутера Listing-сервиса.
type Deps struct {
	Log            *slog.Logger
	Handler        *Handler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Listing-сервиса.
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

		// Создание, правка и действия жизненного цикла — только владелец (из идентичности).
		api.Post("/api/v1/ads", d.Handler.Create)
		api.Patch("/api/v1/ads/{id}", d.Handler.Update)
		api.Delete("/api/v1/ads/{id}", d.Handler.Delete)
		api.Post("/api/v1/ads/{id}/submit", d.Handler.Submit)
		api.Post("/api/v1/ads/{id}/sold", d.Handler.Sold)
		api.Post("/api/v1/ads/{id}/renew", d.Handler.Renew)
		api.Post("/api/v1/ads/{id}/archive", d.Handler.Archive)
		// Мои объявления — только владелец.
		api.Get("/api/v1/me/ads", d.Handler.MyAds)
		// Лента и карточка — публично.
		api.Get("/api/v1/ads", d.Handler.Feed)
		api.Get("/api/v1/ads/{id}", d.Handler.Get)
	})

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r
}
