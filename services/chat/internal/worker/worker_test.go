package worker_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/chat/internal/worker"
)

type fakeStore struct {
	processed   bool
	bannedUser  string
	blockedMsg  string
	markedCount int
}

func (f *fakeStore) BanUser(_ context.Context, userID, _ string) error {
	f.bannedUser = userID
	return nil
}
func (f *fakeStore) BlockMessage(_ context.Context, messageID string) error {
	f.blockedMsg = messageID
	return nil
}
func (f *fakeStore) IsEventProcessed(context.Context, string, string) (bool, error) {
	return f.processed, nil
}
func (f *fakeStore) MarkEventProcessed(context.Context, string, string) error {
	f.markedCount++
	return nil
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHandleBan_ProjectsBan(t *testing.T) {
	store := &fakeStore{}
	m := worker.New(store, discardLog())
	env, err := events.New(events.SubjectUserBanned, "moderation", map[string]string{"user_id": "u1", "reason": "спам"})
	require.NoError(t, err)

	require.NoError(t, m.HandleBan(context.Background(), env))
	assert.Equal(t, "u1", store.bannedUser)
	assert.Equal(t, 1, store.markedCount, "событие отмечено обработанным")
}

func TestHandleBan_AlreadyProcessed_Skips(t *testing.T) {
	store := &fakeStore{processed: true}
	m := worker.New(store, discardLog())
	env, _ := events.New(events.SubjectUserBanned, "moderation", map[string]string{"user_id": "u1"})

	require.NoError(t, m.HandleBan(context.Background(), env))
	assert.Empty(t, store.bannedUser, "повторное событие не выполняет работу")
	assert.Zero(t, store.markedCount)
}

func TestHandleBan_EmptyUserID_Errors(t *testing.T) {
	store := &fakeStore{}
	m := worker.New(store, discardLog())
	env, _ := events.New(events.SubjectUserBanned, "moderation", map[string]string{"reason": "x"})
	assert.Error(t, m.HandleBan(context.Background(), env))
}

func TestHandleBlock_HidesMessage(t *testing.T) {
	store := &fakeStore{}
	m := worker.New(store, discardLog())
	env, err := events.New(events.SubjectChatMessageBlocked, "moderation", map[string]string{"message_id": "m1", "reason": "оскорбление"})
	require.NoError(t, err)

	require.NoError(t, m.HandleBlock(context.Background(), env))
	assert.Equal(t, "m1", store.blockedMsg)
	assert.Equal(t, 1, store.markedCount)
}

func TestHandleBlock_EmptyMessageID_Errors(t *testing.T) {
	store := &fakeStore{}
	m := worker.New(store, discardLog())
	env, _ := events.New(events.SubjectChatMessageBlocked, "moderation", map[string]string{"reason": "x"})
	assert.Error(t, m.HandleBlock(context.Background(), env))
}
