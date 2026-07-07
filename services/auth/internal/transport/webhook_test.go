package transport

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-webhook-secret"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestRouter() http.Handler {
	return NewRouter(Deps{
		Log:     discardLogger(),
		Webhook: NewWebhookHandler(testSecret, discardLogger()),
	})
}

func postWebhook(t *testing.T, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/telegram/webhook", strings.NewReader(body))
	if secret != "" {
		req.Header.Set(telegramSecretHeader, secret)
	}
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, req)
	return rec
}

func TestWebhook_ValidSecretWithContact(t *testing.T) {
	body := `{"update_id":1,"message":{"message_id":2,"from":{"id":42},"contact":{"phone_number":"+998901234567","user_id":42}}}`
	rec := postWebhook(t, testSecret, body)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")
}

func TestWebhook_WrongSecretRejected(t *testing.T) {
	rec := postWebhook(t, "wrong-secret", `{"update_id":1}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	assert.Contains(t, rec.Body.String(), "invalid_webhook_secret")
}

func TestWebhook_MissingSecretRejected(t *testing.T) {
	rec := postWebhook(t, "", `{"update_id":1}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWebhook_MalformedBody(t *testing.T) {
	rec := postWebhook(t, testSecret, `{not json`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_update")
}

func TestWebhook_TolerantToUnknownFields(t *testing.T) {
	// Реальный payload Telegram шире нашей модели — лишние поля не ломают приём.
	body := `{"update_id":5,"unknown_top":{"x":1},"message":{"message_id":9,"weird_field":true,"from":{"id":7,"is_bot":false}}}`
	rec := postWebhook(t, testSecret, body)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRouter_Health(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")
}

func TestRouter_NotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/unknown", nil)
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
}
