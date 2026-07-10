package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"
)

type fakeLifecycleSvc struct {
	suspendN     int
	suspendErr   error
	resumeOK     bool
	resumeErr    error
	resumeTitle  string
	refundOK     bool
	refundErr    error
	refundReason string
	calls        []string
}

func (f *fakeLifecycleSvc) Suspend(_ context.Context, adID string) (int, error) {
	f.calls = append(f.calls, "suspend:"+adID)
	return f.suspendN, f.suspendErr
}

func (f *fakeLifecycleSvc) Resume(_ context.Context, adID, title string) (bool, error) {
	f.calls = append(f.calls, "resume:"+adID)
	f.resumeTitle = title
	return f.resumeOK, f.resumeErr
}

func (f *fakeLifecycleSvc) Refund(_ context.Context, adID, reason string) (bool, error) {
	f.calls = append(f.calls, "refund:"+adID)
	f.refundReason = reason
	return f.refundOK, f.refundErr
}

func lifecycleEnv(t *testing.T, subject, adID, status, title string) events.Envelope {
	t.Helper()
	e, err := events.New(subject, "listing", map[string]any{"ad_id": adID, "status": status, "title": title})
	require.NoError(t, err)
	return e
}

func TestLifecycle_ExpiredSuspends(t *testing.T) {
	f := &fakeLifecycleSvc{suspendN: 2}
	err := NewLifecycle(f, discardLog()).Handle(context.Background(),
		lifecycleEnv(t, events.SubjectAdExpired, "ad-1", "expired", ""))
	require.NoError(t, err)
	assert.Equal(t, []string{"suspend:ad-1"}, f.calls)
}

func TestLifecycle_BlockedRefunds(t *testing.T) {
	f := &fakeLifecycleSvc{refundOK: true}
	err := NewLifecycle(f, discardLog()).Handle(context.Background(),
		lifecycleEnv(t, events.SubjectAdBlocked, "ad-1", "blocked", ""))
	require.NoError(t, err)
	assert.Equal(t, []string{"refund:ad-1"}, f.calls)
	assert.Equal(t, "ad_blocked", f.refundReason)
}

func TestLifecycle_DeletedRefunds(t *testing.T) {
	f := &fakeLifecycleSvc{refundOK: true}
	err := NewLifecycle(f, discardLog()).Handle(context.Background(),
		lifecycleEnv(t, events.SubjectAdDeleted, "ad-1", "", ""))
	require.NoError(t, err)
	assert.Equal(t, []string{"refund:ad-1"}, f.calls)
	assert.Equal(t, "ad_deleted", f.refundReason)
}

func TestLifecycle_UpdatedActiveResumes(t *testing.T) {
	f := &fakeLifecycleSvc{resumeOK: true}
	err := NewLifecycle(f, discardLog()).Handle(context.Background(),
		lifecycleEnv(t, events.SubjectAdUpdated, "ad-1", "active", "Camry 2019"))
	require.NoError(t, err)
	assert.Equal(t, []string{"resume:ad-1"}, f.calls)
	assert.Equal(t, "Camry 2019", f.resumeTitle, "заголовок проброшен для события продвижения")
}

func TestLifecycle_UpdatedNonActiveNoop(t *testing.T) {
	f := &fakeLifecycleSvc{}
	err := NewLifecycle(f, discardLog()).Handle(context.Background(),
		lifecycleEnv(t, events.SubjectAdUpdated, "ad-1", "pending", ""))
	require.NoError(t, err)
	assert.Empty(t, f.calls, "обновление не в active не возобновляет услуги")
}

func TestLifecycle_EmptyAdIDErrors(t *testing.T) {
	err := NewLifecycle(&fakeLifecycleSvc{}, discardLog()).Handle(context.Background(),
		lifecycleEnv(t, events.SubjectAdExpired, "", "expired", ""))
	require.Error(t, err)
}

func TestLifecycle_ErrorPropagates(t *testing.T) {
	f := &fakeLifecycleSvc{suspendErr: errors.New("boom")}
	err := NewLifecycle(f, discardLog()).Handle(context.Background(),
		lifecycleEnv(t, events.SubjectAdExpired, "ad-1", "expired", ""))
	require.Error(t, err)
}
