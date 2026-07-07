package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/authx"
)

func meRouter() http.Handler {
	h := NewMeHandler()
	mux := http.NewServeMux()
	mux.Handle("/api/v1/auth/me", authx.FromForwardedHeaders(http.HandlerFunc(h.Me)))
	return mux
}

func TestMe_Authenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("X-User-Id", "user-42")
	req.Header.Set("X-User-Roles", "user,admin")
	rec := httptest.NewRecorder()
	meRouter().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp meResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "user-42", resp.UserID)
	assert.Equal(t, []string{"user", "admin"}, resp.Roles)
}

func TestMe_Anonymous(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rec := httptest.NewRecorder()
	meRouter().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "unauthorized")
}
