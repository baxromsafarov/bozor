package worker_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/auth/internal/worker"
)

type fakeBanStore struct {
	got string
	n   int64
}

func (s *fakeBanStore) BanUser(_ context.Context, userID string) (int64, error) {
	s.got = userID
	return s.n, nil
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestBanner_RevokesSessions(t *testing.T) {
	store := &fakeBanStore{n: 2}
	b := worker.NewBanner(store, discardLog())

	env, err := events.New(events.SubjectUserBanned, "moderation", map[string]any{
		"user_id": "u1", "type": "permanent", "reason": "мошенник",
	})
	require.NoError(t, err)

	require.NoError(t, b.Handle(context.Background(), env))
	assert.Equal(t, "u1", store.got)
}

func TestBanner_EmptyUserID_Errors(t *testing.T) {
	store := &fakeBanStore{}
	b := worker.NewBanner(store, discardLog())

	env, err := events.New(events.SubjectUserBanned, "moderation", map[string]any{"reason": "x"})
	require.NoError(t, err)

	assert.Error(t, b.Handle(context.Background(), env))
	assert.Empty(t, store.got)
}
