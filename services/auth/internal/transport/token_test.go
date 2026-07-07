package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/apperr"

	"bozor/services/auth/internal/domain"
)

func tokenRouter(h *TokenHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/v1/auth/refresh", h.Refresh)
	return r
}

func postRefresh(t *testing.T, tokens Tokens, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewTokenHandler(tokens, discardLogger())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(body))
	rec := httptest.NewRecorder()
	tokenRouter(h).ServeHTTP(rec, req)
	return rec
}

func TestRefresh_Success(t *testing.T) {
	tokens := &fakeTokens{pair: domain.TokenPair{
		AccessToken: "a", RefreshToken: "r", TokenType: "Bearer", ExpiresIn: 900, UserID: "u1",
	}}
	rec := postRefresh(t, tokens, `{"refresh_token":"old","device_id":"dev-abc12345"}`)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp tokenResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "a", resp.AccessToken)
	assert.Equal(t, "r", resp.RefreshToken)
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.Equal(t, 900, resp.ExpiresIn)
}

func TestRefresh_MissingToken(t *testing.T) {
	rec := postRefresh(t, &fakeTokens{}, `{"device_id":"dev-abc12345"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing_refresh_token")
}

func TestRefresh_BadDeviceID(t *testing.T) {
	rec := postRefresh(t, &fakeTokens{}, `{"refresh_token":"old","device_id":"short"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_device_id")
}

func TestRefresh_Unauthorized(t *testing.T) {
	tokens := &fakeTokens{err: apperr.New(apperr.KindUnauthorized, "token_reuse_detected",
		"Сессия отозвана", "Sessiya bekor qilindi")}
	rec := postRefresh(t, tokens, `{"refresh_token":"old","device_id":"dev-abc12345"}`)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	assert.Contains(t, rec.Body.String(), "token_reuse_detected")
}

func TestRefresh_MalformedBody(t *testing.T) {
	rec := postRefresh(t, &fakeTokens{}, `{not json`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}
