package ratelimit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeAllower управляет исходом проверки лимита.
type fakeAllower struct {
	allowed bool
	retry   time.Duration
	err     error
	calls   int
}

func (f *fakeAllower) Allow(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	f.calls++
	return f.allowed, f.retry, f.err
}

func serve(t *testing.T, a Allower) *httptest.ResponseRecorder {
	t.Helper()
	var nextCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})
	mw := Middleware(a, "init", 5, time.Minute, ClientIPKey, discardLogger())
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "203.0.113.1:12345"
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	t.Cleanup(func() { _ = nextCalled })
	return rec
}

func TestMiddleware_Allowed(t *testing.T) {
	rec := serve(t, &fakeAllower{allowed: true})
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMiddleware_Denied(t *testing.T) {
	rec := serve(t, &fakeAllower{allowed: false, retry: 30 * time.Second})
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "30", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	assert.Contains(t, rec.Body.String(), "rate_limited")
}

func TestMiddleware_FailOpenOnError(t *testing.T) {
	rec := serve(t, &fakeAllower{err: errors.New("redis down")})
	assert.Equal(t, http.StatusOK, rec.Code, "при ошибке Redis запрос пропускается (fail-open)")
}

func TestClientIPKey(t *testing.T) {
	cases := map[string]struct {
		xff, xrip, remote, want string
	}{
		"xff first hop": {xff: "1.2.3.4, 5.6.7.8", want: "1.2.3.4"},
		"x-real-ip":     {xrip: "9.9.9.9", want: "9.9.9.9"},
		"remote addr":   {remote: "8.8.8.8:443", want: "8.8.8.8"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.xff != "" {
				req.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.xrip != "" {
				req.Header.Set("X-Real-Ip", c.xrip)
			}
			if c.remote != "" {
				req.RemoteAddr = c.remote
			}
			require.Equal(t, c.want, ClientIPKey(req))
		})
	}
}
