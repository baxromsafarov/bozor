// Package transport собирает HTTP-слой Auth-сервиса: middleware-цепочку из
// pkg/shared, служебные эндпоинты и маршруты Auth API. Пути идут под
// /api/v1/auth/* — так их проксирует gateway.
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

const serviceName = "auth"

// Middleware — псевдоним для HTTP-миддлвари (rate-limit и т.п.).
type Middleware = func(http.Handler) http.Handler

// Deps — зависимости для сборки роутера Auth-сервиса.
type Deps struct {
	Log            *slog.Logger
	Webhook        *WebhookHandler
	Session        *SessionHandler
	Token          *TokenHandler
	Me             *MeHandler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler

	// Пер-роутовые лимитеры (nil — без ограничения).
	InitRateLimit    Middleware
	WebhookRateLimit Middleware
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
		// Идентичность, проброшенная gateway после проверки access-JWT.
		auth.Use(authx.FromForwardedHeaders)

		// Вебхук Telegram: подлинность — по X-Telegram-Bot-Api-Secret-Token
		// (rate-limit по IP как защита от флуда доставок).
		applyMW(auth, d.WebhookRateLimit).Post("/telegram/webhook", d.Webhook.Handle)

		// Логин по nonce/deep-link: клиент инициирует вход и опрашивает статус.
		if d.Session != nil {
			applyMW(auth, d.InitRateLimit).Post("/session/init", d.Session.Init)
			auth.Get("/session/{nonce}", d.Session.Status)
		}
		// Ротация refresh-токена и выход (отзыв семейства).
		if d.Token != nil {
			auth.Post("/refresh", d.Token.Refresh)
			auth.Post("/logout", d.Token.Logout)
		}
		// Идентичность текущего пользователя (защищённый маршрут).
		if d.Me != nil {
			auth.Get("/me", d.Me.Me)
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

// applyMW возвращает роутер с применённой миддлварью mw либо исходный при nil.
func applyMW(r chi.Router, mw Middleware) chi.Router {
	if mw == nil {
		return r
	}
	return r.With(mw)
}
