package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/listing/internal/app"
	"bozor/services/listing/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func decisionEnv(t *testing.T, subject, adID string) events.Envelope {
	t.Helper()
	e, err := events.New(subject, "moderation", map[string]any{"ad_id": adID})
	require.NoError(t, err)
	return e
}

// --- Moderator ---

type fakeModStore struct {
	processed bool
	ad        domain.Ad
	getErr    error
	marked    int
	applied   *domain.StatusUpdate
	appliedEv events.Envelope
}

func (f *fakeModStore) IsEventProcessed(context.Context, string, string) (bool, error) {
	return f.processed, nil
}

func (f *fakeModStore) MarkEventProcessed(context.Context, string, string) error {
	f.marked++
	return nil
}

func (f *fakeModStore) GetByID(context.Context, string) (domain.Ad, error) {
	if f.getErr != nil {
		return domain.Ad{}, f.getErr
	}
	return f.ad, nil
}

func (f *fakeModStore) ApplyModerationWithEvent(_ context.Context, _, _, _ string, upd domain.StatusUpdate, ev events.Envelope) error {
	f.applied = &upd
	f.appliedEv = ev
	return nil
}

func pendingAd() domain.Ad {
	return domain.Ad{ID: "ad-1", UserID: "user-1", CategoryID: "cat", Title: "BMW", Price: 1,
		Currency: "UZS", RegionID: 1, Status: domain.StatusPending}
}

func TestModerator_Approve(t *testing.T) {
	store := &fakeModStore{ad: pendingAd()}
	err := NewModerator(store, 720*time.Hour, discardLogger()).
		Handle(context.Background(), decisionEnv(t, events.SubjectAdApproved, "ad-1"))
	require.NoError(t, err)
	require.NotNil(t, store.applied, "переход применён")
	assert.Equal(t, domain.StatusPending, store.applied.From)
	assert.Equal(t, domain.StatusActive, store.applied.To)
	require.NotNil(t, store.applied.PublishedAt, "активация проставляет published_at")
	require.NotNil(t, store.applied.ExpiresAt, "активация проставляет expires_at")
	assert.Equal(t, events.SubjectAdUpdated, store.appliedEv.Type)
	assert.Zero(t, store.marked)

	var payload app.AdEvent
	require.NoError(t, store.appliedEv.Decode(&payload))
	assert.Equal(t, "active", payload.Status)
}

func TestModerator_Reject(t *testing.T) {
	store := &fakeModStore{ad: pendingAd()}
	err := NewModerator(store, 720*time.Hour, discardLogger()).
		Handle(context.Background(), decisionEnv(t, events.SubjectAdRejected, "ad-1"))
	require.NoError(t, err)
	require.NotNil(t, store.applied)
	assert.Equal(t, domain.StatusRejected, store.applied.To)
	assert.Nil(t, store.applied.PublishedAt, "отклонение не публикует объявление")
}

func TestModerator_BlockActiveAd(t *testing.T) {
	ad := pendingAd()
	ad.Status = domain.StatusActive
	store := &fakeModStore{ad: ad}
	err := NewModerator(store, 720*time.Hour, discardLogger()).
		Handle(context.Background(), decisionEnv(t, events.SubjectAdBlocked, "ad-1"))
	require.NoError(t, err)
	require.NotNil(t, store.applied, "снятие применяется к активному объявлению")
	assert.Equal(t, domain.StatusActive, store.applied.From, "снятие идёт из текущего статуса")
	assert.Equal(t, domain.StatusBlocked, store.applied.To)
	assert.Zero(t, store.marked)

	var payload app.AdEvent
	require.NoError(t, store.appliedEv.Decode(&payload))
	assert.Equal(t, "blocked", payload.Status)
}

func TestModerator_BlockAlreadyBlockedMarksProcessed(t *testing.T) {
	ad := pendingAd()
	ad.Status = domain.StatusBlocked
	store := &fakeModStore{ad: ad}
	err := NewModerator(store, 720*time.Hour, discardLogger()).
		Handle(context.Background(), decisionEnv(t, events.SubjectAdBlocked, "ad-1"))
	require.NoError(t, err)
	assert.Nil(t, store.applied, "повторное снятие не выполняет работу")
	assert.Equal(t, 1, store.marked)
}

func TestModerator_AlreadyProcessedSkips(t *testing.T) {
	store := &fakeModStore{processed: true, ad: pendingAd()}
	err := NewModerator(store, 720*time.Hour, discardLogger()).
		Handle(context.Background(), decisionEnv(t, events.SubjectAdApproved, "ad-1"))
	require.NoError(t, err)
	assert.Nil(t, store.applied, "обработанное событие не применяется повторно")
	assert.Zero(t, store.marked)
}

func TestModerator_NotPendingMarksProcessed(t *testing.T) {
	ad := pendingAd()
	ad.Status = domain.StatusActive
	store := &fakeModStore{ad: ad}
	err := NewModerator(store, 720*time.Hour, discardLogger()).
		Handle(context.Background(), decisionEnv(t, events.SubjectAdApproved, "ad-1"))
	require.NoError(t, err)
	assert.Nil(t, store.applied, "не в модерации — переход не выполняется")
	assert.Equal(t, 1, store.marked, "событие отмечено обработанным")
}

func TestModerator_AdMissingMarksProcessed(t *testing.T) {
	store := &fakeModStore{getErr: domain.ErrAdNotFound}
	err := NewModerator(store, 720*time.Hour, discardLogger()).
		Handle(context.Background(), decisionEnv(t, events.SubjectAdApproved, "ad-1"))
	require.NoError(t, err)
	assert.Nil(t, store.applied)
	assert.Equal(t, 1, store.marked)
}

func TestModerator_UnexpectedTypeErrors(t *testing.T) {
	store := &fakeModStore{ad: pendingAd()}
	err := NewModerator(store, 720*time.Hour, discardLogger()).
		Handle(context.Background(), decisionEnv(t, events.SubjectAdBumped, "ad-1"))
	require.Error(t, err)
	assert.Nil(t, store.applied)
}

func TestModerator_EmptyAdIDErrors(t *testing.T) {
	store := &fakeModStore{ad: pendingAd()}
	err := NewModerator(store, 720*time.Hour, discardLogger()).
		Handle(context.Background(), decisionEnv(t, events.SubjectAdApproved, ""))
	require.Error(t, err)
}

// --- Expirer ---

type fakeExpiryStore struct {
	list     []domain.Ad
	listErr  error
	expireOK map[string]bool
	expired  []string
	events   []events.Envelope
}

func (f *fakeExpiryStore) ListExpired(context.Context, time.Time, int) ([]domain.Ad, error) {
	return f.list, f.listErr
}

func (f *fakeExpiryStore) ExpireWithEvent(_ context.Context, adID string, ev events.Envelope) (bool, error) {
	f.expired = append(f.expired, adID)
	f.events = append(f.events, ev)
	return f.expireOK[adID], nil
}

func TestExpirer_Sweep(t *testing.T) {
	a1 := domain.Ad{ID: "a1", UserID: "u", CategoryID: "c", Title: "x", Currency: "UZS", RegionID: 1, Status: domain.StatusActive}
	a2 := domain.Ad{ID: "a2", UserID: "u", CategoryID: "c", Title: "y", Currency: "UZS", RegionID: 1, Status: domain.StatusActive}
	store := &fakeExpiryStore{list: []domain.Ad{a1, a2}, expireOK: map[string]bool{"a1": true, "a2": false}}

	NewExpirer(store, time.Minute, 100, discardLogger()).Sweep(context.Background())

	assert.Equal(t, []string{"a1", "a2"}, store.expired, "оба кандидата обработаны")
	require.Len(t, store.events, 2)
	assert.Equal(t, events.SubjectAdExpired, store.events[0].Type)

	var payload app.AdEvent
	require.NoError(t, store.events[0].Decode(&payload))
	assert.Equal(t, "expired", payload.Status, "событие несёт статус expired")
}

func TestExpirer_SweepListError(t *testing.T) {
	store := &fakeExpiryStore{listErr: assertErr}
	NewExpirer(store, time.Minute, 100, discardLogger()).Sweep(context.Background())
	assert.Empty(t, store.expired, "ошибка выборки — ничего не истекает")
}

var assertErr = errStub("boom")

type errStub string

func (e errStub) Error() string { return string(e) }
