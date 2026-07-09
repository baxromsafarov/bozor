package notify_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/notification/internal/domain"
	"bozor/services/notification/internal/notify"
)

type fakeRecipientStore struct {
	last domain.Recipient
	n    int
}

func (s *fakeRecipientStore) UpsertRecipient(_ context.Context, rec domain.Recipient) error {
	s.last = rec
	s.n++
	return nil
}

func TestRecipients_Projects(t *testing.T) {
	store := &fakeRecipientStore{}
	c := notify.NewRecipients(store, discardLog())

	env, err := events.New(events.SubjectUserCreated, "auth", map[string]any{
		"user_id": "u1", "telegram_user_id": 12345, "language_code": "uz-Latn",
	})
	require.NoError(t, err)

	require.NoError(t, c.Handle(context.Background(), env))
	assert.Equal(t, 1, store.n)
	assert.Equal(t, "u1", store.last.UserID)
	assert.EqualValues(t, 12345, store.last.TelegramUserID)
	assert.Equal(t, "uz", store.last.LanguageCode) // нормализован
}

func TestRecipients_MissingFields(t *testing.T) {
	store := &fakeRecipientStore{}
	c := notify.NewRecipients(store, discardLog())

	env, err := events.New(events.SubjectUserCreated, "auth", map[string]any{
		"user_id": "u1", "telegram_user_id": 0, // нет chat_id
	})
	require.NoError(t, err)

	assert.Error(t, c.Handle(context.Background(), env))
	assert.Zero(t, store.n)
}
