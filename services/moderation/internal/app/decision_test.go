package app_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/moderation/internal/app"
	"bozor/services/moderation/internal/domain"
)

type fakeStore struct {
	task    domain.Task
	found   bool
	applied bool

	gotAdID, gotStatus, gotComment, gotModerator string
	publishedType                                string
	payload                                      map[string]any
	decideCalls                                  int
}

func (s *fakeStore) GetTask(context.Context, string) (domain.Task, bool, error) {
	return s.task, s.found, nil
}

func (s *fakeStore) DecideWithEvent(_ context.Context, adID, newStatus, moderatorID, comment string, ev events.Envelope) (bool, error) {
	s.decideCalls++
	s.gotAdID, s.gotStatus, s.gotComment, s.gotModerator = adID, newStatus, comment, moderatorID
	s.publishedType = ev.Type
	_ = ev.Decode(&s.payload)
	return s.applied, nil
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func manualTask() domain.Task {
	return domain.Task{AdID: "ad1", UserID: "u1", Title: "iPhone", Status: domain.StatusManual}
}

func TestApprove(t *testing.T) {
	store := &fakeStore{task: manualTask(), found: true, applied: true}
	svc := app.NewService(store, discardLog())

	require.NoError(t, svc.Approve(context.Background(), "ad1", "mod1"))
	assert.Equal(t, domain.StatusApproved, store.gotStatus)
	assert.Equal(t, "mod1", store.gotModerator)
	assert.Equal(t, events.SubjectAdApproved, store.publishedType)
	assert.Equal(t, "ad1", store.payload["ad_id"])
	assert.Equal(t, "u1", store.payload["user_id"])
}

func TestReject(t *testing.T) {
	store := &fakeStore{task: manualTask(), found: true, applied: true}
	svc := app.NewService(store, discardLog())

	require.NoError(t, svc.Reject(context.Background(), "ad1", "mod1", "стоп-слово"))
	assert.Equal(t, domain.StatusRejected, store.gotStatus)
	assert.Equal(t, "стоп-слово", store.gotComment)
	assert.Equal(t, events.SubjectAdRejected, store.publishedType)
	assert.Equal(t, "стоп-слово", store.payload["reason"])
	assert.Equal(t, false, store.payload["request_edit"])
}

func TestRequestEdit(t *testing.T) {
	store := &fakeStore{task: manualTask(), found: true, applied: true}
	svc := app.NewService(store, discardLog())

	require.NoError(t, svc.RequestEdit(context.Background(), "ad1", "mod1", "уточните цену"))
	assert.Equal(t, domain.StatusEditRequested, store.gotStatus)
	assert.Equal(t, events.SubjectAdRejected, store.publishedType)
	assert.Equal(t, true, store.payload["request_edit"])
}

func TestReject_EmptyReason(t *testing.T) {
	store := &fakeStore{task: manualTask(), found: true, applied: true}
	svc := app.NewService(store, discardLog())

	err := svc.Reject(context.Background(), "ad1", "mod1", "   ")
	assert.ErrorIs(t, err, domain.ErrReasonRequired)
	assert.Zero(t, store.decideCalls) // до стора не дошли
}

func TestDecide_TaskNotFound(t *testing.T) {
	store := &fakeStore{found: false}
	svc := app.NewService(store, discardLog())
	assert.ErrorIs(t, svc.Approve(context.Background(), "ad1", "mod1"), app.ErrTaskNotFound)
}

func TestDecide_AlreadyDecided(t *testing.T) {
	task := manualTask()
	task.Status = domain.StatusApproved // уже решена
	store := &fakeStore{task: task, found: true}
	svc := app.NewService(store, discardLog())

	assert.ErrorIs(t, svc.Reject(context.Background(), "ad1", "mod1", "причина"), app.ErrNotPending)
	assert.Zero(t, store.decideCalls)
}

func TestDecide_RaceLostToConcurrentDecision(t *testing.T) {
	// Задача была manual при чтении, но условный UPDATE не затронул строк.
	store := &fakeStore{task: manualTask(), found: true, applied: false}
	svc := app.NewService(store, discardLog())

	assert.ErrorIs(t, svc.Approve(context.Background(), "ad1", "mod1"), app.ErrNotPending)
	assert.Equal(t, 1, store.decideCalls)
}
