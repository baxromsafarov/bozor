package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/auth/internal/session"
)

// fakeSessionStore подменяет Redis-хранилище в тестах HTTP-слоя.
type fakeSessionStore struct {
	nonce   string
	ttl     time.Duration
	initErr error
	got     session.Session
	getErr  error
}

func (f *fakeSessionStore) Init(context.Context) (string, time.Duration, error) {
	return f.nonce, f.ttl, f.initErr
}

func (f *fakeSessionStore) Get(context.Context, string) (session.Session, error) {
	return f.got, f.getErr
}

func sessionRouter(h *SessionHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/v1/auth/session/init", h.Init)
	r.Get("/api/v1/auth/session/{nonce}", h.Status)
	return r
}

func TestSessionInit_ReturnsNonceAndDeepLink(t *testing.T) {
	store := &fakeSessionStore{nonce: "0123456789abcdef0123456789abcdef", ttl: 3 * time.Minute}
	h := NewSessionHandler(store, "bozor_bot", discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session/init", nil)
	rec := httptest.NewRecorder()
	sessionRouter(h).ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp initResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, store.nonce, resp.Nonce)
	assert.Equal(t, "https://t.me/bozor_bot?start="+store.nonce, resp.DeepLink)
	assert.Equal(t, 180, resp.ExpiresIn)
}

func TestSessionInit_EmptyBotUsername_NoDeepLink(t *testing.T) {
	store := &fakeSessionStore{nonce: "0123456789abcdef0123456789abcdef", ttl: 3 * time.Minute}
	h := NewSessionHandler(store, "", discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session/init", nil)
	rec := httptest.NewRecorder()
	sessionRouter(h).ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp initResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp.DeepLink, "без username бота ссылка не формируется")
}

func TestSessionStatus_Pending(t *testing.T) {
	store := &fakeSessionStore{got: session.Session{Status: session.StatusPending}}
	h := NewSessionHandler(store, "bozor_bot", discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session/0123456789abcdef0123456789abcdef", nil)
	rec := httptest.NewRecorder()
	sessionRouter(h).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, session.StatusPending, resp.Status)
	assert.Empty(t, resp.UserID)
}

func TestSessionStatus_Confirmed(t *testing.T) {
	store := &fakeSessionStore{got: session.Session{Status: session.StatusConfirmed, UserID: "u-1"}}
	h := NewSessionHandler(store, "bozor_bot", discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session/0123456789abcdef0123456789abcdef", nil)
	rec := httptest.NewRecorder()
	sessionRouter(h).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, session.StatusConfirmed, resp.Status)
	assert.Equal(t, "u-1", resp.UserID)
}

func TestSessionStatus_RejectsMalformedNonce(t *testing.T) {
	store := &fakeSessionStore{}
	h := NewSessionHandler(store, "bozor_bot", discardLogger())

	// Слишком короткий и с недопустимыми символами nonce.
	for _, bad := range []string{"short", "ZZZ", strings.Repeat("g", 32)} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session/"+bad, nil)
		rec := httptest.NewRecorder()
		sessionRouter(h).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "nonce %q должен отклоняться", bad)
		assert.Contains(t, rec.Body.String(), "invalid_nonce")
	}
}
