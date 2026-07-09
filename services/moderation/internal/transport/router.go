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

const serviceName = "moderation"

// Deps — зависимости для сборки роутера Moderation-сервиса.
type Deps struct {
	Log            *slog.Logger
	Handler        *Handler
	Decision       *DecisionHandler
	Reports        *ReportHandler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Moderation-сервиса.
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

	// Подача жалобы — любой авторизованный пользователь (не только персонал).
	if d.Reports != nil {
		r.Group(func(api chi.Router) {
			api.Use(authx.FromForwardedHeaders)
			api.Post("/api/v1/reports", d.Reports.Create)
		})
	}

	r.Group(func(api chi.Router) {
		api.Use(authx.FromForwardedHeaders)
		api.Use(requireStaff)
		// Очередь модерации — только персонал (admin/moderator).
		api.Get("/api/v1/moderation/tasks", d.Handler.ListTasks)
		// Ручные действия модератора над задачами очереди.
		if d.Decision != nil {
			api.Post("/api/v1/moderation/tasks/{adId}/approve", d.Decision.Approve)
			api.Post("/api/v1/moderation/tasks/{adId}/reject", d.Decision.Reject)
			api.Post("/api/v1/moderation/tasks/{adId}/request-edit", d.Decision.RequestEdit)
		}
		// Жалобы и баны — только персонал.
		if d.Reports != nil {
			api.Get("/api/v1/moderation/reports", d.Reports.List)
			api.Post("/api/v1/moderation/reports/{id}/resolve", d.Reports.Resolve)
			api.Post("/api/v1/moderation/bans", d.Reports.Ban)
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

// requireStaff пропускает только аутентифицированный персонал (admin/moderator).
// Аноним → 401, недостаточно прав → 403.
func requireStaff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authx.UserID(r.Context()) == "" {
			httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
				"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
			return
		}
		if !authx.HasRole(r.Context(), "admin") && !authx.HasRole(r.Context(), "moderator") {
			httpx.WriteProblem(w, r, apperr.New(apperr.KindForbidden, "forbidden",
				"Доступ запрещён", "Ruxsat berilmagan"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
