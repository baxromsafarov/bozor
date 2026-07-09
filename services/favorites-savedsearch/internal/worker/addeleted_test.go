package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	removed []string
	n       int64
	err     error
}

func (f *fakeStore) RemoveByAd(_ context.Context, adID string) (int64, error) {
	f.removed = append(f.removed, adID)
	return f.n, f.err
}

func deletedEvent(t *testing.T, adID string) events.Envelope {
	t.Helper()
	ev, err := events.New(events.SubjectAdDeleted, "listing", map[string]string{"ad_id": adID})
	require.NoError(t, err)
	return ev
}

func TestAdDeleted_Handle_RemovesByAd(t *testing.T) {
	store := &fakeStore{n: 2}
	err := NewAdDeleted(store, discardLogger()).Handle(context.Background(), deletedEvent(t, "ad-1"))
	require.NoError(t, err)
	assert.Equal(t, []string{"ad-1"}, store.removed)
}

func TestAdDeleted_Handle_EmptyAdID(t *testing.T) {
	err := NewAdDeleted(&fakeStore{}, discardLogger()).Handle(context.Background(), deletedEvent(t, ""))
	require.Error(t, err)
}

func TestAdDeleted_Handle_StoreError(t *testing.T) {
	store := &fakeStore{err: errors.New("db down")}
	err := NewAdDeleted(store, discardLogger()).Handle(context.Background(), deletedEvent(t, "ad-1"))
	require.Error(t, err, "ошибка БД → повтор/DLQ")
}
