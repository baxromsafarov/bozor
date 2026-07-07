package transport

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/auth/internal/app"
	"bozor/services/auth/internal/domain"
)

const testSecret = "test-webhook-secret"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeBot фиксирует исходящие вызовы Telegram.
type fakeBot struct {
	contactReqs int
	texts       []string
}

func (b *fakeBot) SendContactRequest(context.Context, int64, string, string) error {
	b.contactReqs++
	return nil
}

func (b *fakeBot) SendText(_ context.Context, _ int64, text string) error {
	b.texts = append(b.texts, text)
	return nil
}

// fakeStore фиксирует, дошло ли дело до персистентности.
type fakeStore struct {
	called  bool
	created bool
}

func (s *fakeStore) UpsertUserWithEvent(context.Context, domain.User, events.Envelope) (string, bool, error) {
	s.called = true
	return "00000000-0000-7000-8000-000000000001", s.created, nil
}

// linkCall фиксирует один вызов Link.
type linkCall struct {
	nonce string
	tgID  int64
}

// fakeSessions фиксирует привязку/подтверждение логин-сессий.
type fakeSessions struct {
	linked        []linkCall
	linkErr       error
	confirmedFor  int64
	confirmUserID string
	confirmNonce  string
	confirmErr    error
}

func (f *fakeSessions) Link(_ context.Context, nonce string, tgID int64) error {
	f.linked = append(f.linked, linkCall{nonce, tgID})
	return f.linkErr
}

func (f *fakeSessions) Confirm(_ context.Context, tgID int64, userID string) (string, error) {
	f.confirmedFor = tgID
	f.confirmUserID = userID
	return f.confirmNonce, f.confirmErr
}

func serve(t *testing.T, bot Bot, store app.Store, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewWebhookHandler(testSecret, app.NewService(store), bot, &fakeSessions{}, discardLogger())
	router := NewRouter(Deps{Log: discardLogger(), Webhook: h})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/telegram/webhook", strings.NewReader(body))
	if secret != "" {
		req.Header.Set(telegramSecretHeader, secret)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestWebhook_ValidContactRegistered(t *testing.T) {
	bot := &fakeBot{}
	store := &fakeStore{created: true}
	body := `{"update_id":1,"message":{"message_id":2,"from":{"id":42,"language_code":"ru"},"contact":{"phone_number":"+998901234567","user_id":42}}}`
	rec := serve(t, bot, store, testSecret, body)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, store.called, "контакт своего владельца регистрируется")
	require.NotEmpty(t, bot.texts)
	assert.Contains(t, bot.texts[0], "подтверждён")
}

// Критично для безопасности: пересланный чужой контакт не регистрируется.
func TestWebhook_ForwardedContactRejected(t *testing.T) {
	bot := &fakeBot{}
	store := &fakeStore{}
	body := `{"update_id":1,"message":{"message_id":2,"from":{"id":42,"language_code":"ru"},"contact":{"phone_number":"+998901234567","user_id":999}}}`
	rec := serve(t, bot, store, testSecret, body)

	assert.Equal(t, http.StatusOK, rec.Code, "вебхук всегда 2xx для Telegram")
	assert.False(t, store.called, "чужой контакт не доходит до персистентности")
	require.NotEmpty(t, bot.texts)
	assert.Contains(t, bot.texts[0], "свой номер")
}

func TestWebhook_StartSendsContactButton(t *testing.T) {
	bot := &fakeBot{}
	body := `{"update_id":3,"message":{"message_id":4,"from":{"id":7,"language_code":"uz"},"text":"/start"}}`
	rec := serve(t, bot, &fakeStore{}, testSecret, body)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, bot.contactReqs, "на /start бот шлёт кнопку запроса контакта")
}

// serveWith прогоняет один webhook с заданными bot/store/sessions.
func serveWith(t *testing.T, bot Bot, store app.Store, sessions SessionLinker, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewWebhookHandler(testSecret, app.NewService(store), bot, sessions, discardLogger())
	router := NewRouter(Deps{Log: discardLogger(), Webhook: h})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/telegram/webhook", strings.NewReader(body))
	req.Header.Set(telegramSecretHeader, testSecret)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// /start <nonce> из deep-link привязывает сессию к Telegram-пользователю.
func TestWebhook_StartWithNonceLinksSession(t *testing.T) {
	bot := &fakeBot{}
	sess := &fakeSessions{}
	body := `{"update_id":3,"message":{"message_id":4,"from":{"id":7,"language_code":"uz"},"text":"/start abc123nonce"}}`
	rec := serveWith(t, bot, &fakeStore{}, sess, body)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, bot.contactReqs, "бот всё равно шлёт кнопку контакта")
	require.Len(t, sess.linked, 1)
	assert.Equal(t, int64(7), sess.linked[0].tgID)
	assert.Equal(t, "abc123nonce", sess.linked[0].nonce)
}

// Контакт от пользователя, пришедшего по deep-link, подтверждает его сессию.
func TestWebhook_ContactConfirmsSession(t *testing.T) {
	bot := &fakeBot{}
	sess := &fakeSessions{confirmNonce: "abc123nonce"}
	body := `{"update_id":1,"message":{"message_id":2,"from":{"id":42,"language_code":"ru"},"contact":{"phone_number":"+998901234567","user_id":42}}}`
	rec := serveWith(t, bot, &fakeStore{created: true}, sess, body)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int64(42), sess.confirmedFor, "сессия подтверждена для отправителя")
	assert.Equal(t, "00000000-0000-7000-8000-000000000001", sess.confirmUserID, "передан id пользователя")
	require.NotEmpty(t, bot.texts)
	assert.Contains(t, bot.texts[0], "Вход подтверждён", "при deep-link-входе особое сообщение")
}

// Обычный контакт без deep-link: Confirm вернул пустой nonce — обычное подтверждение.
func TestWebhook_ContactWithoutSessionPlainConfirm(t *testing.T) {
	bot := &fakeBot{}
	sess := &fakeSessions{confirmNonce: ""}
	body := `{"update_id":1,"message":{"message_id":2,"from":{"id":42,"language_code":"ru"},"contact":{"phone_number":"+998901234567","user_id":42}}}`
	rec := serveWith(t, bot, &fakeStore{created: true}, sess, body)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, bot.texts)
	assert.Contains(t, bot.texts[0], "подтверждён")
}

func TestWebhook_WrongSecretRejected(t *testing.T) {
	rec := serve(t, &fakeBot{}, &fakeStore{}, "wrong", `{"update_id":1}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	assert.Contains(t, rec.Body.String(), "invalid_webhook_secret")
}

func TestWebhook_MissingSecretRejected(t *testing.T) {
	rec := serve(t, &fakeBot{}, &fakeStore{}, "", `{"update_id":1}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWebhook_MalformedBody(t *testing.T) {
	rec := serve(t, &fakeBot{}, &fakeStore{}, testSecret, `{not json`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_update")
}

func TestWebhook_TolerantToUnknownFields(t *testing.T) {
	body := `{"update_id":5,"unknown_top":{"x":1},"message":{"message_id":9,"weird":true,"from":{"id":7,"is_bot":false}}}`
	rec := serve(t, &fakeBot{}, &fakeStore{}, testSecret, body)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRouter_Health(t *testing.T) {
	h := NewWebhookHandler(testSecret, app.NewService(&fakeStore{}), &fakeBot{}, &fakeSessions{}, discardLogger())
	router := NewRouter(Deps{Log: discardLogger(), Webhook: h})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")
}

func TestRouter_NotFound(t *testing.T) {
	h := NewWebhookHandler(testSecret, app.NewService(&fakeStore{}), &fakeBot{}, &fakeSessions{}, discardLogger())
	router := NewRouter(Deps{Log: discardLogger(), Webhook: h})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/unknown", nil)
	r := httptest.NewRecorder()
	router.ServeHTTP(r, req)
	assert.Equal(t, http.StatusNotFound, r.Code)
	assert.Contains(t, r.Header().Get("Content-Type"), "application/problem+json")
}
