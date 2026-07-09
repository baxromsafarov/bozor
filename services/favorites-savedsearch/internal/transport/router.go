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

const serviceName = "favorites-savedsearch"

// Deps — зависимости для сборки роутера Favorites/SavedSearch-сервиса.
type Deps struct {
	Log            *slog.Logger
	Handler        *Handler
	SavedSearch    *SavedSearchHandler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Favorites/SavedSearch-сервиса.
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

		// Избранное — только владелец (из идентичности).
		api.Post("/api/v1/favorites/{adId}", d.Handler.Add)
		api.Delete("/api/v1/favorites/{adId}", d.Handler.Remove)
		api.Get("/api/v1/me/favorites", d.Handler.List)

		// Сохранённые поиски — только владелец.
		if d.SavedSearch != nil {
			api.Post("/api/v1/saved-searches", d.SavedSearch.Create)
			api.Get("/api/v1/me/saved-searches", d.SavedSearch.List)
			api.Delete("/api/v1/saved-searches/{id}", d.SavedSearch.Delete)
		}
	})

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r
}
