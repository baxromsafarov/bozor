package otelx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
)

func TestSetup(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "пустой endpoint — no-op shutdown", endpoint: ""},
		{name: "endpoint только из пробелов", endpoint: "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tt.endpoint)

			shutdown, err := Setup(context.Background(), "test-service")
			require.NoError(t, err)
			require.NotNil(t, shutdown)

			// shutdown не должен паниковать и не должен возвращать ошибку.
			assert.NotPanics(t, func() {
				assert.NoError(t, shutdown(context.Background()))
			})

			// Пропагатор должен быть установлен всегда.
			fields := otel.GetTextMapPropagator().Fields()
			assert.Contains(t, fields, "traceparent")
			assert.Contains(t, fields, "baggage")
		})
	}
}

func TestSetupMetrics(t *testing.T) {
	handler, shutdown, err := SetupMetrics("test-service")
	require.NoError(t, err)
	require.NotNil(t, handler)
	require.NotNil(t, shutdown)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	// Стандартные коллекторы Go/process должны присутствовать.
	body := rec.Body.String()
	assert.Contains(t, body, "go_goroutines")
	assert.Contains(t, body, "process_")

	assert.NotPanics(t, func() {
		assert.NoError(t, shutdown(context.Background()))
	})
}

func TestHTTPMiddleware(t *testing.T) {
	mw := HTTPMiddleware("test-service")
	require.NotNil(t, mw)

	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}))
	require.NotNil(t, h)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.True(t, called)
	assert.Equal(t, http.StatusTeapot, rec.Code)
}
