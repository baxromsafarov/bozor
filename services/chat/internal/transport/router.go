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

const serviceName = "chat"

// Deps — зависимости для сборки роутера Chat-сервиса.
type Deps struct {
	Log            *slog.Logger
	Conversations  *ConversationHandler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Chat-сервиса.
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

	// Чат — любой аутентифицированный пользователь (доступ к своим диалогам).
	if d.Conversations != nil {
		r.Group(func(api chi.Router) {
			api.Use(authx.FromForwardedHeaders)
			api.Use(requireAuth)
			api.Post("/api/v1/conversations", d.Conversations.Start)
			api.Get("/api/v1/conversations", d.Conversations.List)
			api.Get("/api/v1/conversations/{id}/messages", d.Conversations.Messages)
			api.Post("/api/v1/conversations/{id}/messages", d.Conversations.Send)
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
