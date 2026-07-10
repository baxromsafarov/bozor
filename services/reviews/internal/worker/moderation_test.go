package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	processed bool
	blocked   []string
	marked    int
}

func (f *fakeStore) BlockReview(_ context.Context, reviewID string) error {
	f.blocked = append(f.blocked, reviewID)
	return nil
}
func (f *fakeStore) IsEventProcessed(context.Context, string, string) (bool, error) {
	return f.processed, nil
}
func (f *fakeStore) MarkEventProcessed(context.Context, string, string) error {
	f.marked++
	return nil
}

func blockEnv(t *testing.T, reviewID string) events.Envelope {
	t.Helper()
	e, err := events.New(events.SubjectReviewBlocked, "moderation", map[string]any{"review_id": reviewID, "reason": "спам"})
	require.NoError(t, err)
	return e
}

func TestHandleBlock_HidesReview(t *testing.T) {
	store := &fakeStore{}
	err := New(store, discardLog()).HandleBlock(context.Background(), blockEnv(t, "rev-1"))
	require.NoError(t, err)
	assert.Equal(t, []string{"rev-1"}, store.blocked)
	assert.Equal(t, 1, store.marked, "событие отмечено обработанным (inbox)")
}

func TestHandleBlock_AlreadyProcessedSkips(t *testing.T) {
	store := &fakeStore{processed: true}
	err := New(store, discardLog()).HandleBlock(context.Background(), blockEnv(t, "rev-1"))
	require.NoError(t, err)
	assert.Empty(t, store.blocked, "обработанное событие не снимает отзыв повторно")
	assert.Zero(t, store.marked)
}

func TestHandleBlock_EmptyIDErrors(t *testing.T) {
	err := New(&fakeStore{}, discardLog()).HandleBlock(context.Background(), blockEnv(t, ""))
	require.Error(t, err)
}
