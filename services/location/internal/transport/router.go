// Package transport собирает HTTP-слой Location-сервиса. Публичные маршруты
// идут под /api/v1/locations/* — так их проксирует gateway.
package transport

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/otelx"
)

const serviceName = "location"

// Deps — зависимости для сборки роутера Location-сервиса.
type Deps struct {
	Log            *slog.Logger
	Handler        *Handler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Location-сервиса.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.Recover(d.Log))
	r.Use(httpx.RequestID)
	r.Use(httpx.Lang)
	r.Use(httpx.AccessLog(d.Log))
	r.Use(otelx.HTTPMiddleware(serviceName))

	// Служебные эндпоинты (health/ready/metrics не проксируются наружу).
	r.Get("/healthz", httpx.HealthHandler())
	r.Get("/readyz", httpx.ReadyHandler(d.ReadyChecks))
	if d.MetricsHandler != nil {
		r.Handle("/metrics", d.MetricsHandler)
	}

	r.Route("/api/v1/locations", func(loc chi.Router) {
		loc.Get("/regions", d.Handler.Regions)
		loc.Get("/regions/{regionID}/cities", d.Handler.Cities)
	})

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r
}
