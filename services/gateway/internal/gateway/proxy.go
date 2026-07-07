package gateway

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/logging"
)

// headerRequestID — заголовок сквозного идентификатора запроса.
const headerRequestID = "X-Request-Id"

// newProxyTransport создаёт транспорт для проксирования к апстримам:
// разумные таймауты плюс автоматическая otel-трассировка исходящих вызовов.
func newProxyTransport() http.RoundTripper {
	base := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return otelhttp.NewTransport(base)
}

// newReverseProxy строит обратный прокси к target. Проставляет X-Forwarded-*
// и сквозной X-Request-Id, а недоступность апстрима превращает в ответ
// 503 RFC 7807 (а не в голый 502 от http-стека).
func newReverseProxy(target *url.URL, transport http.RoundTripper, log *slog.Logger) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			if id := logging.RequestID(pr.In.Context()); id != "" {
				pr.Out.Header.Set(headerRequestID, id)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.WarnContext(r.Context(), "апстрим недоступен",
				slog.String("target", target.String()),
				slog.String("path", r.URL.Path),
				slog.String("error", err.Error()),
			)
			httpx.WriteProblem(w, r, apperr.New(apperr.KindUnavailable, "upstream_unavailable",
				"Сервис временно недоступен", "Xizmat vaqtincha mavjud emas"))
		},
	}
}
