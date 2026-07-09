package notify_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/notification/internal/domain"
	"bozor/services/notification/internal/notify"
	"bozor/services/notification/internal/telegram"
)

// --- фейки ---

type notifRow struct {
	status, reason, body string
	attempts             int
}

type fakeStore struct {
	recipients map[string]domain.Recipient
	rows       map[string]*notifRow
}

func newFakeStore() *fakeStore {
	return &fakeStore{recipients: map[string]domain.Recipient{}, rows: map[string]*notifRow{}}
}

func (s *fakeStore) GetRecipient(_ context.Context, userID string) (domain.Recipient, bool, error) {
	r, ok := s.recipients[userID]
	return r, ok, nil
}

func (s *fakeStore) RecordSkipped(_ context.Context, _, eventID, _, _ string, _ []byte, reason string) error {
	if _, ok := s.rows[eventID]; !ok {
		s.rows[eventID] = &notifRow{status: domain.StatusSkipped, reason: reason}
	}
	return nil
}

func (s *fakeStore) BeginDelivery(_ context.Context, _, eventID, _, _ string, _ []byte, body string) (bool, error) {
	r, ok := s.rows[eventID]
	if !ok {
		s.rows[eventID] = &notifRow{status: domain.StatusPending, attempts: 1, body: body}
		return true, nil
	}
	r.attempts++
	r.body = body
	return r.status == domain.StatusPending, nil
}

func (s *fakeStore) MarkSent(_ context.Context, eventID string) error {
	s.rows[eventID].status = domain.StatusSent
	return nil
}

func (s *fakeStore) MarkFailed(_ context.Context, eventID, reason string) error {
	s.rows[eventID].status = domain.StatusFailed
	s.rows[eventID].reason = reason
	return nil
}

func (s *fakeStore) MarkSkipped(_ context.Context, eventID, reason string) error {
	s.rows[eventID].status = domain.StatusSkipped
	s.rows[eventID].reason = reason
	return nil
}

type fakePrefs struct {
	enabled bool
	err     error
}

func (p fakePrefs) Enabled(_ context.Context, _, group string) (bool, error) {
	if group == domain.GroupNone {
		return true, nil
	}
	return p.enabled, p.err
}

type fakeSender struct {
	err      error
	calls    int
	lastChat int64
	lastText string
}

func (s *fakeSender) Send(_ context.Context, chatID int64, text string) error {
	s.calls++
	s.lastChat = chatID
	s.lastText = text
	return s.err
}

type noopWaiter struct{}

func (noopWaiter) Wait(context.Context) error { return nil }

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func makeEnv(t *testing.T, subject string, p domain.EventPayload) events.Envelope {
	t.Helper()
	env, err := events.New(subject, "test", p)
	require.NoError(t, err)
	return env
}

// seedRecipient регистрирует получателя u1 (chat_id 555, язык ru).
func seedRecipient(s *fakeStore) {
	s.recipients["u1"] = domain.Recipient{UserID: "u1", TelegramUserID: 555, LanguageCode: "ru"}
}

// --- тесты ---

func TestHandle_HappyPath(t *testing.T) {
	store := newFakeStore()
	seedRecipient(store)
	sender := &fakeSender{}
	h := notify.NewHandler(store, fakePrefs{enabled: true}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectAdApproved, domain.EventPayload{UserID: "u1", Title: "Camry"})
	require.NoError(t, h.Handle(context.Background(), env))

	assert.Equal(t, 1, sender.calls)
	assert.EqualValues(t, 555, sender.lastChat)
	assert.Contains(t, sender.lastText, "Camry")
	assert.Equal(t, domain.StatusSent, store.rows[env.ID].status)
}

func TestHandle_PrefsDisabled_Skips(t *testing.T) {
	store := newFakeStore()
	seedRecipient(store)
	sender := &fakeSender{}
	h := notify.NewHandler(store, fakePrefs{enabled: false}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectSavedSearchMatched, domain.EventPayload{UserID: "u1", Name: "поиск"})
	require.NoError(t, h.Handle(context.Background(), env))

	assert.Zero(t, sender.calls)
	assert.Equal(t, domain.StatusSkipped, store.rows[env.ID].status)
	assert.Equal(t, domain.ReasonPrefsDisabled, store.rows[env.ID].reason)
}

func TestHandle_BanBypassesPrefs(t *testing.T) {
	store := newFakeStore()
	seedRecipient(store)
	sender := &fakeSender{}
	// prefs.enabled=false, но бан (GroupNone) уведомляется всегда.
	h := notify.NewHandler(store, fakePrefs{enabled: false}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectUserBanned, domain.EventPayload{UserID: "u1", Reason: "спам"})
	require.NoError(t, h.Handle(context.Background(), env))

	assert.Equal(t, 1, sender.calls)
	assert.Contains(t, sender.lastText, "спам")
	assert.Equal(t, domain.StatusSent, store.rows[env.ID].status)
}

func TestHandle_NoRecipient_RetriesWithError(t *testing.T) {
	store := newFakeStore() // получатель не спроецирован
	sender := &fakeSender{}
	h := notify.NewHandler(store, fakePrefs{enabled: true}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectAdApproved, domain.EventPayload{UserID: "ghost", Title: "X"})
	err := h.Handle(context.Background(), env)

	require.Error(t, err) // повторяемо (Nak)
	assert.Zero(t, sender.calls)
	assert.Empty(t, store.rows) // ни skipped, ни pending — ждём проекцию
}

func TestHandle_ChannelDisabled_Skips(t *testing.T) {
	store := newFakeStore()
	seedRecipient(store)
	sender := &fakeSender{}
	h := notify.NewHandler(store, fakePrefs{enabled: true}, sender, noopWaiter{}, false, discardLog())

	env := makeEnv(t, events.SubjectAdExpired, domain.EventPayload{UserID: "u1", Title: "X"})
	require.NoError(t, h.Handle(context.Background(), env))

	assert.Zero(t, sender.calls)
	assert.Equal(t, domain.StatusSkipped, store.rows[env.ID].status)
	assert.Equal(t, domain.ReasonChannelDisabled, store.rows[env.ID].reason)
}

func TestHandle_NoUserID_Acks(t *testing.T) {
	store := newFakeStore()
	sender := &fakeSender{}
	h := notify.NewHandler(store, fakePrefs{enabled: true}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectReviewCreated, domain.EventPayload{}) // нет user_id
	require.NoError(t, h.Handle(context.Background(), env))
	assert.Zero(t, sender.calls)
	assert.Empty(t, store.rows)
}

func TestHandle_PermanentSendError_Fails(t *testing.T) {
	store := newFakeStore()
	seedRecipient(store)
	sender := &fakeSender{err: &telegram.SendError{Code: 403, Description: "blocked", Retryable: false}}
	h := notify.NewHandler(store, fakePrefs{enabled: true}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectAdApproved, domain.EventPayload{UserID: "u1", Title: "X"})
	require.NoError(t, h.Handle(context.Background(), env)) // Ack (терминально)

	assert.Equal(t, domain.StatusFailed, store.rows[env.ID].status)
	assert.Contains(t, store.rows[env.ID].reason, "blocked")
}

func TestHandle_RetryableSendError_Naks(t *testing.T) {
	store := newFakeStore()
	seedRecipient(store)
	sender := &fakeSender{err: &telegram.SendError{Code: 429, Retryable: true}}
	h := notify.NewHandler(store, fakePrefs{enabled: true}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectAdApproved, domain.EventPayload{UserID: "u1", Title: "X"})
	err := h.Handle(context.Background(), env)

	require.Error(t, err) // Nak → повтор
	assert.Equal(t, domain.StatusPending, store.rows[env.ID].status)
	assert.Equal(t, 1, store.rows[env.ID].attempts)
}

func TestHandle_Idempotent_NoDoubleSend(t *testing.T) {
	store := newFakeStore()
	seedRecipient(store)
	sender := &fakeSender{}
	h := notify.NewHandler(store, fakePrefs{enabled: true}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectAdApproved, domain.EventPayload{UserID: "u1", Title: "X"})
	require.NoError(t, h.Handle(context.Background(), env))
	require.NoError(t, h.Handle(context.Background(), env)) // повторная доставка того же события

	assert.Equal(t, 1, sender.calls) // отправлено ровно один раз
	assert.Equal(t, domain.StatusSent, store.rows[env.ID].status)
}

func TestHandle_RetryThenSucceed_Attempts(t *testing.T) {
	store := newFakeStore()
	seedRecipient(store)
	sender := &fakeSender{err: &telegram.SendError{Code: 500, Retryable: true}}
	h := notify.NewHandler(store, fakePrefs{enabled: true}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectAdApproved, domain.EventPayload{UserID: "u1", Title: "X"})
	require.Error(t, h.Handle(context.Background(), env)) // 1-я попытка провалилась

	sender.err = nil                                        // канал восстановился
	require.NoError(t, h.Handle(context.Background(), env)) // повторная доставка

	assert.Equal(t, domain.StatusSent, store.rows[env.ID].status)
	assert.Equal(t, 2, store.rows[env.ID].attempts)
	assert.Equal(t, 2, sender.calls)
}

func TestHandle_PrefsError_Retries(t *testing.T) {
	store := newFakeStore()
	seedRecipient(store)
	sender := &fakeSender{}
	h := notify.NewHandler(store, fakePrefs{err: assertErr{}}, sender, noopWaiter{}, true, discardLog())

	env := makeEnv(t, events.SubjectAdApproved, domain.EventPayload{UserID: "u1", Title: "X"})
	assert.Error(t, h.Handle(context.Background(), env)) // недоступность prefs → Nak
	assert.Zero(t, sender.calls)
}

type assertErr struct{}

func (assertErr) Error() string { return "prefs down" }
