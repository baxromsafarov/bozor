package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/user-profile/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	processed map[string]bool
	created   []domain.Profile
}

func newFakeStore() *fakeStore { return &fakeStore{processed: map[string]bool{}} }

func (f *fakeStore) IsEventProcessed(_ context.Context, consumer, eventID string) (bool, error) {
	return f.processed[consumer+"|"+eventID], nil
}

func (f *fakeStore) CreateProfileWithInbox(_ context.Context, p domain.Profile, consumer, eventID string) error {
	f.processed[consumer+"|"+eventID] = true
	f.created = append(f.created, p)
	return nil
}

func userCreatedEvent(t *testing.T, userID, lang string) events.Envelope {
	t.Helper()
	ev, err := events.New(events.SubjectUserCreated, "auth", map[string]any{
		"user_id": userID, "language_code": lang,
	})
	require.NoError(t, err)
	return ev
}

func TestUsers_Handle_CreatesProfile(t *testing.T) {
	store := newFakeStore()
	err := NewUsers(store, discardLogger()).Handle(context.Background(), userCreatedEvent(t, "u1", "uz"))
	require.NoError(t, err)
	require.Len(t, store.created, 1)
	assert.Equal(t, "u1", store.created[0].UserID)
	assert.Equal(t, "uz", store.created[0].LanguageCode)
	assert.Equal(t, domain.UserTypeIndividual, store.created[0].UserType)
}

func TestUsers_Handle_IdempotentSkip(t *testing.T) {
	store := newFakeStore()
	u := NewUsers(store, discardLogger())
	ev := userCreatedEvent(t, "u1", "ru")

	require.NoError(t, u.Handle(context.Background(), ev))
	require.NoError(t, u.Handle(context.Background(), ev), "повтор того же события")
	assert.Len(t, store.created, 1, "профиль создан один раз")
}

func TestUsers_Handle_EmptyUserID(t *testing.T) {
	err := NewUsers(newFakeStore(), discardLogger()).Handle(context.Background(), userCreatedEvent(t, "", "ru"))
	require.Error(t, err)
}
