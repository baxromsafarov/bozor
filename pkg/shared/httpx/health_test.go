package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler(t *testing.T) {
	w := httptest.NewRecorder()
	HealthHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
}

func TestReadyHandler(t *testing.T) {
	okCheck := func(ctx context.Context) error { return nil }
	failCheck := func(ctx context.Context) error { return errors.New("нет соединения с БД") }

	tests := []struct {
		name       string
		checks     map[string]Check
		wantStatus int
		wantBody   string
		wantChecks map[string]string
	}{
		{
			name:       "все проверки ок",
			checks:     map[string]Check{"postgres": okCheck, "nats": okCheck},
			wantStatus: http.StatusOK,
			wantBody:   "ready",
		},
		{
			name:       "без проверок — готов",
			checks:     nil,
			wantStatus: http.StatusOK,
			wantBody:   "ready",
		},
		{
			name:       "одна проверка падает",
			checks:     map[string]Check{"postgres": okCheck, "redis": failCheck},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "unavailable",
			wantChecks: map[string]string{"postgres": "ok", "redis": "нет соединения с БД"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ReadyHandler(tt.checks).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			assert.Equal(t, tt.wantStatus, w.Code)

			var body struct {
				Status string            `json:"status"`
				Checks map[string]string `json:"checks"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, tt.wantBody, body.Status)
			if tt.wantChecks != nil {
				assert.Equal(t, tt.wantChecks, body.Checks)
			}
		})
	}
}
