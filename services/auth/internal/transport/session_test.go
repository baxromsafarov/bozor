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

	"bozor/services/auth/internal/domain"
	"bozor/services/auth/internal/session"
)

// fakeSessionStore подменяет Redis-хранилище в тестах HTTP-слоя.
type fakeSessionStore struct {
	nonce      string
	ttl        time.Duration
	initErr    error
	got        session.Session
	getErr     error
	consumed   session.Session // что вернёт Consume (по умолчанию = got)
	consumeSet bool
	consumeErr error
}

func (f *fakeSessionStore) Init(context.Context) (string, time.Duration, error) {
	return f.nonce, f.ttl, f.initErr
}

func (f *fakeSessionStore) Get(context.Context, string) (session.Session, error) {
	return f.got, f.getErr
}

func (f *fakeSessionStore) Consume(context.Context, string) (session.Session, error) {
	if f.consumeSet {
		return f.consumed, f.consumeErr
	}
	return f.got, f.consumeErr
}

// fakeTokens подменяет выпуск/ротацию/отзыв токенов.
type fakeTokens struct {
	gotUserID   string
	gotDeviceID string
	gotIP       string
	logoutToken string
	pair        domain.TokenPair
	err         error
	logoutErr   error
}

func (f *fakeTokens) IssueForUser(_ context.Context, userID, deviceID, clientIP string) (domain.TokenPair, error) {
	f.gotUserID = userID
	f.gotDeviceID = deviceID
	f.gotIP = clientIP
	return f.pair, f.err
}

func (f *fakeTokens) Refresh(_ context.Context, _, _, clientIP string) (domain.TokenPair, error) {
	f.gotIP = clientIP
	return f.pair, f.err
}

func (f *fakeTokens) Logout(_ context.Context, refreshToken, clientIP string) error {
	f.logoutToken = refreshToken
	f.gotIP = clientIP
	return f.logoutErr
}

func sessionRouter(h *SessionHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/v1/auth/session/init", h.Init)
	r.Get("/api/v1/auth/session/{nonce}", h.Status)
	return r
}

func TestSessionInit_ReturnsNonceAndDeepLink(t *testing.T) {
	store := &fakeSessionStore{nonce: "0123456789abcdef0123456789abcdef", ttl: 3 * time.Minute}
	h := NewSessionHandler(store, &fakeTokens{}, "bozor_bot", discardLogger())

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
	h := NewSessionHandler(store, &fakeTokens{}, "", discardLogger())

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
	h := NewSessionHandler(store, &fakeTokens{}, "bozor_bot", discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session/0123456789abcdef0123456789abcdef", nil)
	rec := httptest.NewRecorder()
	sessionRouter(h).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, session.StatusPending, resp.Status)
	assert.Empty(t, resp.UserID)
	assert.Empty(t, resp.AccessToken, "pending не выдаёт токены")
}

// Подтверждённый вход выдаёт пару токенов и пробрасывает device_id из запроса.
func TestSessionStatus_ConfirmedIssuesTokens(t *testing.T) {
	store := &fakeSessionStore{got: session.Session{Status: session.StatusConfirmed, UserID: "u-1"}}
	tokens := &fakeTokens{pair: domain.TokenPair{
		AccessToken: "acc", RefreshToken: "ref", TokenType: "Bearer",
		ExpiresIn: 900, DeviceID: "dev-abc12345", UserID: "u-1",
	}}
	h := NewSessionHandler(store, tokens, "bozor_bot", discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session/0123456789abcdef0123456789abcdef?device_id=dev-abc12345", nil)
	rec := httptest.NewRecorder()
	sessionRouter(h).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, session.StatusConfirmed, resp.Status)
	assert.Equal(t, "u-1", resp.UserID)
	assert.Equal(t, "acc", resp.AccessToken)
	assert.Equal(t, "ref", resp.RefreshToken)
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.Equal(t, 900, resp.ExpiresIn)
	assert.Equal(t, "dev-abc12345", tokens.gotDeviceID, "device_id проброшен в выпуск токена")
}

// Гонку опроса проиграл текущий запрос: Consume вернул пусто → expired.
func TestSessionStatus_ConfirmedButConsumedRace(t *testing.T) {
	store := &fakeSessionStore{
		got:        session.Session{Status: session.StatusConfirmed, UserID: "u-1"},
		consumeSet: true,
		consumed:   session.Session{Status: session.StatusExpired},
	}
	h := NewSessionHandler(store, &fakeTokens{}, "bozor_bot", discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session/0123456789abcdef0123456789abcdef", nil)
	rec := httptest.NewRecorder()
	sessionRouter(h).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, session.StatusExpired, resp.Status)
	assert.Empty(t, resp.AccessToken)
}

// Невалидный device_id отклоняется до потребления nonce.
func TestSessionStatus_RejectsBadDeviceID(t *testing.T) {
	store := &fakeSessionStore{got: session.Session{Status: session.StatusConfirmed, UserID: "u-1"}}
	h := NewSessionHandler(store, &fakeTokens{}, "bozor_bot", discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session/0123456789abcdef0123456789abcdef?device_id=%21bad", nil)
	rec := httptest.NewRecorder()
	sessionRouter(h).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_device_id")
}

func TestSessionStatus_RejectsMalformedNonce(t *testing.T) {
	store := &fakeSessionStore{}
	h := NewSessionHandler(store, &fakeTokens{}, "bozor_bot", discardLogger())

	// Слишком короткий и с недопустимыми символами nonce.
	for _, bad := range []string{"short", "ZZZ", strings.Repeat("g", 32)} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session/"+bad, nil)
		rec := httptest.NewRecorder()
		sessionRouter(h).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "nonce %q должен отклоняться", bad)
		assert.Contains(t, rec.Body.String(), "invalid_nonce")
	}
}
