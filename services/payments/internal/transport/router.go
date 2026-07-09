// Package transport собирает HTTP-слой Payments/Promotions-сервиса. Публичный
// каталог услуг идёт под /api/v1/promotions/* — так его проксирует gateway.
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

const serviceName = "payments-promotions"

// Deps — зависимости для сборки роутера сервиса.
type Deps struct {
	Log            *slog.Logger
	Catalog        *CatalogHandler
	Wallet         *WalletHandler
	ReadyChecks    map[string]httpx.Check
	MetricsHandler http.Handler
}

// NewRouter собирает HTTP-роутер Payments/Promotions-сервиса.
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

	// Каталог платных услуг — публичный (цены зависят от региона/категории query).
	if d.Catalog != nil {
		r.Get("/api/v1/promotions/catalog", d.Catalog.Get)
	}

	// Кошелёк — только аутентифицированный пользователь (свой баланс/история).
	if d.Wallet != nil {
		r.Group(func(api chi.Router) {
			api.Use(authx.FromForwardedHeaders)
			api.Use(requireAuth)
			api.Get("/api/v1/me/wallet", d.Wallet.Get)
			api.Get("/api/v1/me/wallet/transactions", d.Wallet.Transactions)
			api.Post("/api/v1/wallet/topup", d.Wallet.Topup)
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
