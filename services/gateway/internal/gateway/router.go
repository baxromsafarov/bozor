// Package gateway собирает HTTP-роутер API Gateway: middleware-цепочку
// (recover, request-id, логирование, otel, CORS, auth, rate-limit) и
// проксирование внешнего REST `/api/v1/*` на внутренние сервисы.
// Бизнес-логики не содержит.
package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/otelx"

	"bozor/services/gateway/internal/ratelimit"
)

// serviceName — имя сервиса для трассировки/логов.
const serviceName = "gateway"

// Deps — зависимости для сборки роутера.
type Deps struct {
	Log            *slog.Logger
	JWTKey         []byte
	Limiter        ratelimit.Limiter
	RateRPS        float64
	RateBurst      int
	AllowedOrigins []string
	Upstream       func(service string) string // резолвер базового URL сервиса
	ReadyChecks    map[string]httpx.Check
}

// NewRouter собирает роутер API Gateway. Возвращает ошибку, если базовый
// URL какого-либо апстрима не парсится.
func NewRouter(d Deps) (http.Handler, error) {
	proxies, err := buildProxies(d)
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()

	// Глобальная middleware-цепочка (порядок из RULES §3).
	r.Use(httpx.Recover(d.Log))
	r.Use(httpx.RequestID)
	r.Use(httpx.Lang)
	r.Use(httpx.AccessLog(d.Log))
	r.Use(otelx.HTTPMiddleware(serviceName))
	r.Use(CORS(d.AllowedOrigins))
	r.Use(StripIdentityHeaders)

	// Служебные эндпоинты самого gateway (не проксируются).
	r.Get("/healthz", httpx.HealthHandler())
	r.Get("/readyz", httpx.ReadyHandler(d.ReadyChecks))

	// Внешний API: проверка JWT (если передан) → rate-limit → проксирование.
	r.Route("/api/v1", func(api chi.Router) {
		api.Use(OptionalAuth(d.JWTKey))
		api.Use(RateLimit(d.Limiter, d.RateRPS, d.RateBurst, d.Log))
		for _, rt := range Routes {
			sub := strings.TrimPrefix(rt.Prefix, "/api/v1")
			proxy := proxies[rt.Service]
			api.Handle(sub, proxy)
			api.Handle(sub+"/*", proxy)
		}
	})

	notFound := func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteProblem(w, req, apperr.New(apperr.KindNotFound, "not_found",
			"Ресурс не найден", "Resurs topilmadi"))
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	return r, nil
}

// buildProxies создаёт по одному обратному прокси на каждый уникальный
// сервис из таблицы маршрутов.
func buildProxies(d Deps) (map[string]http.Handler, error) {
	transport := newProxyTransport()
	proxies := make(map[string]http.Handler)
	for _, rt := range Routes {
		if _, ok := proxies[rt.Service]; ok {
			continue
		}
		target, err := url.Parse(d.Upstream(rt.Service))
		if err != nil {
			return nil, fmt.Errorf("gateway: некорректный upstream для %s: %w", rt.Service, err)
		}
		proxies[rt.Service] = newReverseProxy(target, transport, d.Log)
	}
	return proxies, nil
}
