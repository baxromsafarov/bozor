// Package transport собирает HTTP-слой Search-сервиса: служебные эндпоинты и
// публичный API поиска (GET /api/v1/ads/search[/facets], Stage 4.3).
package transport

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/otelx"
)

const serviceName = "search"

// Deps — зависимости для сборки роутера Search-сервиса.
type Deps struct {
	Log            *slog.Logger
	Search         Searcher
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Search-сервиса.
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

	// Публичный API поиска (полный путь с /api/v1 — версионирование сервисом).
	if d.Search != nil {
		sh := NewSearchHandler(d.Search, d.Log)
		r.Get("/api/v1/ads/search", sh.Search)
		r.Get("/api/v1/ads/search/facets", sh.Facets)
	}

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r
}
