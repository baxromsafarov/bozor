package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/authx"

	"bozor/services/gateway/internal/ratelimit"
)

// testKey — секрет подписи JWT в тестах.
var testKey = []byte("test-signing-key")

// fakeLimiter — управляемый лимитер для тестов.
type fakeLimiter struct {
	res ratelimit.Result
	err error
}

func (f fakeLimiter) Allow(context.Context, string, float64, int) (ratelimit.Result, error) {
	return f.res, f.err
}

// echoBackend — апстрим, отражающий путь/заголовки в ответ, со счётчиком вызовов.
type echoBackend struct {
	server *httptest.Server
	calls  atomic.Int64
}

func newEchoBackend() *echoBackend {
	b := &echoBackend{}
	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.calls.Add(1)
		h := w.Header()
		h.Set("X-Echo-Path", r.URL.Path)
		h.Set("X-Echo-Query", r.URL.RawQuery)
		h.Set("X-Echo-User", r.Header.Get(headerUserID))
		h.Set("X-Echo-Roles", r.Header.Get(headerUserRoles))
		h.Set("X-Echo-Reqid", r.Header.Get(headerRequestID))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	return b
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestRouter собирает роутер, проксирующий все сервисы на upstream.
func newTestRouter(t *testing.T, limiter ratelimit.Limiter, upstream string) http.Handler {
	t.Helper()
	allow := limiter
	if allow == nil {
		allow = fakeLimiter{res: ratelimit.Result{Allowed: true, Remaining: 39}}
	}
	h, err := NewRouter(Deps{
		Log:            discardLogger(),
		JWTKey:         testKey,
		Limiter:        allow,
		RateRPS:        20,
		RateBurst:      40,
		AllowedOrigins: []string{"*"},
		Upstream:       func(string) string { return upstream },
	})
	require.NoError(t, err)
	return h
}

func TestRouter_ProxiesToUpstream(t *testing.T) {
	backend := newEchoBackend()
	defer backend.server.Close()
	router := newTestRouter(t, nil, backend.server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ads/42?q=phone", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/api/v1/ads/42", rec.Header().Get("X-Echo-Path"), "апстрим получает полный путь")
	assert.Equal(t, "q=phone", rec.Header().Get("X-Echo-Query"))
	assert.NotEmpty(t, rec.Header().Get("X-Echo-Reqid"), "X-Request-Id пробрасывается в апстрим")
	assert.NotEmpty(t, rec.Header().Get("X-Request-Id"), "X-Request-Id есть и в ответе клиенту")
	assert.Equal(t, int64(1), backend.calls.Load())
}

func TestRouter_ForwardsVerifiedIdentity(t *testing.T) {
	backend := newEchoBackend()
	defer backend.server.Close()
	router := newTestRouter(t, nil, backend.server.URL)

	token, _, _, err := authx.NewSigner(testKey, "auth", time.Hour).Sign("user-123", []string{"user", "moderator"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/ads", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(headerUserID, "attacker") // попытка подделки — должна быть вычищена
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "user-123", rec.Header().Get("X-Echo-User"), "идентичность — из проверенного JWT")
	assert.Equal(t, "user,moderator", rec.Header().Get("X-Echo-Roles"))
}

func TestRouter_StripsClientIdentityWhenAnonymous(t *testing.T) {
	backend := newEchoBackend()
	defer backend.server.Close()
	router := newTestRouter(t, nil, backend.server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ads", nil)
	req.Header.Set(headerUserID, "attacker")
	req.Header.Set(headerUserRoles, "admin")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("X-Echo-User"), "подделанный X-User-Id вычищен")
	assert.Empty(t, rec.Header().Get("X-Echo-Roles"), "подделанный X-User-Roles вычищен")
}

func TestRouter_InvalidTokenRejected(t *testing.T) {
	backend := newEchoBackend()
	defer backend.server.Close()
	router := newTestRouter(t, nil, backend.server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ads", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	assert.Equal(t, int64(0), backend.calls.Load(), "невалидный токен не доходит до апстрима")
}

func TestRouter_RateLimited(t *testing.T) {
	backend := newEchoBackend()
	defer backend.server.Close()
	limiter := fakeLimiter{res: ratelimit.Result{Allowed: false, Remaining: 0, ResetSec: 3}}
	router := newTestRouter(t, limiter, backend.server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=x", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "40", rec.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", rec.Header().Get("X-RateLimit-Remaining"))
	assert.Equal(t, "3", rec.Header().Get("X-RateLimit-Reset"))
	assert.Equal(t, "3", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	assert.Equal(t, int64(0), backend.calls.Load())
}

func TestRouter_RateLimiterFailsOpen(t *testing.T) {
	backend := newEchoBackend()
	defer backend.server.Close()
	limiter := fakeLimiter{err: errors.New("redis down")}
	router := newTestRouter(t, limiter, backend.server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ads", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "недоступность лимитера не роняет запрос")
	assert.Equal(t, int64(1), backend.calls.Load())
}

func TestRouter_CORSPreflight(t *testing.T) {
	backend := newEchoBackend()
	defer backend.server.Close()
	router := newTestRouter(t, nil, backend.server.URL)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/ads", nil)
	req.Header.Set("Origin", "https://bozor.uz")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
	assert.Equal(t, int64(0), backend.calls.Load(), "preflight не идёт в апстрим")
}

func TestRouter_NotFound(t *testing.T) {
	backend := newEchoBackend()
	defer backend.server.Close()
	router := newTestRouter(t, nil, backend.server.URL)

	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
}

func TestRouter_Health(t *testing.T) {
	backend := newEchoBackend()
	defer backend.server.Close()
	router := newTestRouter(t, nil, backend.server.URL)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")
}

func TestRouter_MetricsMounted(t *testing.T) {
	stub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "# HELP up\n")
	})
	h, err := NewRouter(Deps{
		Log:            discardLogger(),
		Limiter:        fakeLimiter{res: ratelimit.Result{Allowed: true}},
		AllowedOrigins: []string{"*"},
		Upstream:       func(string) string { return "http://127.0.0.1:1" },
		MetricsHandler: stub,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "# HELP")
}

func TestRouter_UpstreamUnavailable(t *testing.T) {
	// Апстрим указывает на закрытый порт — proxy обязан вернуть 503 RFC 7807.
	router := newTestRouter(t, nil, "http://127.0.0.1:1")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ads", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	assert.Contains(t, rec.Body.String(), "upstream_unavailable")
}

func TestRouter_InvalidUpstreamConfig(t *testing.T) {
	_, err := NewRouter(Deps{
		Log:      discardLogger(),
		Limiter:  fakeLimiter{res: ratelimit.Result{Allowed: true}},
		Upstream: func(string) string { return "://bad-url" },
	})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "upstream")
}
