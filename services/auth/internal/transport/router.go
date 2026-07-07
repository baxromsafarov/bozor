// Package transport собирает HTTP-слой Auth-сервиса: middleware-цепочку из
// pkg/shared, служебные эндпоинты и маршруты Auth API. Пути идут под
// /api/v1/auth/* — так их проксирует gateway.
package transport

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/otelx"
)

const serviceName = "auth"

// Deps — зависимости для сборки роутера Auth-сервиса.
type Deps struct {
	Log            *slog.Logger
	Webhook        *WebhookHandler
	Session        *SessionHandler
	Token          *TokenHandler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Auth-сервиса.
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

	r.Route("/api/v1/auth", func(auth chi.Router) {
		// Вебхук Telegram: подлинность — по X-Telegram-Bot-Api-Secret-Token.
		auth.Post("/telegram/webhook", d.Webhook.Handle)

		// Логин по nonce/deep-link: клиент инициирует вход и опрашивает статус.
		if d.Session != nil {
			auth.Post("/session/init", d.Session.Init)
			auth.Get("/session/{nonce}", d.Session.Status)
		}
		// Ротация refresh-токена на новую пару.
		if d.Token != nil {
			auth.Post("/refresh", d.Token.Refresh)
		}
		// logout — Stage 1.5.
	})

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r
}
