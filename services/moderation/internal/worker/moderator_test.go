package worker_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/moderation/internal/domain"
	"bozor/services/moderation/internal/worker"
)

// --- фейки ---

type fakeSource struct {
	ad    domain.AdView
	found bool
}

func (s fakeSource) GetAd(context.Context, string) (domain.AdView, bool, error) {
	return s.ad, s.found, nil
}

type fakeStore struct {
	stopwords    []string
	forbidden    map[string]bool
	duplicate    bool
	processed    map[string]bool
	approvedTask *domain.Task
	manualTask   *domain.Task
	published    *events.Envelope
	markedOnly   bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{forbidden: map[string]bool{}, processed: map[string]bool{}}
}

func (s *fakeStore) AlreadyProcessed(_ context.Context, _, eventID string) (bool, error) {
	return s.processed[eventID], nil
}
func (s *fakeStore) ActiveStopwords(context.Context) ([]string, error) { return s.stopwords, nil }
func (s *fakeStore) IsForbiddenCategory(_ context.Context, cat string) (bool, error) {
	return s.forbidden[cat], nil
}
func (s *fakeStore) HasDuplicate(context.Context, string, string, string) (bool, error) {
	return s.duplicate, nil
}
func (s *fakeStore) SaveApprovedTask(_ context.Context, _, eventID string, t domain.Task, ev events.Envelope) error {
	s.approvedTask = &t
	s.published = &ev
	s.processed[eventID] = true
	return nil
}
func (s *fakeStore) SaveManualTask(_ context.Context, _, eventID string, t domain.Task) error {
	s.manualTask = &t
	s.processed[eventID] = true
	return nil
}
func (s *fakeStore) MarkProcessed(_ context.Context, _, eventID string) error {
	s.processed[eventID] = true
	s.markedOnly = true
	return nil
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func pendingAd() domain.AdView {
	return domain.AdView{
		ID: "ad1", UserID: "u1", CategoryID: "cat1",
		Title: "iPhone 15 Pro", Description: "Новый, гарантия", Status: "pending",
	}
}

func adEnvelope(t *testing.T, adID, status string) events.Envelope {
	t.Helper()
	env, err := events.New(events.SubjectAdUpdated, "listing",
		map[string]string{"ad_id": adID, "status": status})
	require.NoError(t, err)
	return env
}

// --- тесты ---

func TestHandle_CleanAd_AutoApproves(t *testing.T) {
	store := newFakeStore()
	store.stopwords = []string{"копия", "реплика"}
	m := worker.New(fakeSource{ad: pendingAd(), found: true}, store, discardLog())

	require.NoError(t, m.Handle(context.Background(), adEnvelope(t, "ad1", "pending")))

	require.NotNil(t, store.approvedTask)
	assert.Equal(t, domain.StatusApproved, store.approvedTask.Status)
	assert.Equal(t, domain.AutoPassed, store.approvedTask.AutoResult)
	assert.Empty(t, store.approvedTask.Reasons)
	require.NotNil(t, store.published)
	assert.Equal(t, events.SubjectAdApproved, store.published.Type)
	assert.Nil(t, store.manualTask)
}

func TestHandle_Stopword_ToManualQueue(t *testing.T) {
	store := newFakeStore()
	store.stopwords = []string{"копия"}
	ad := pendingAd()
	ad.Title = "Копия iPhone"
	m := worker.New(fakeSource{ad: ad, found: true}, store, discardLog())

	require.NoError(t, m.Handle(context.Background(), adEnvelope(t, "ad1", "pending")))

	require.NotNil(t, store.manualTask)
	assert.Equal(t, domain.StatusManual, store.manualTask.Status)
	assert.Equal(t, domain.AutoFlagged, store.manualTask.AutoResult)
	assert.Contains(t, store.manualTask.Reasons, domain.ReasonStopwordPrefix+"копия")
	assert.Nil(t, store.approvedTask)
	assert.Nil(t, store.published)
}

func TestHandle_ForbiddenCategory_ToManualQueue(t *testing.T) {
	store := newFakeStore()
	store.forbidden["cat1"] = true
	m := worker.New(fakeSource{ad: pendingAd(), found: true}, store, discardLog())

	require.NoError(t, m.Handle(context.Background(), adEnvelope(t, "ad1", "pending")))
	require.NotNil(t, store.manualTask)
	assert.Contains(t, store.manualTask.Reasons, domain.ReasonForbiddenCat)
}

func TestHandle_Duplicate_ToManualQueue(t *testing.T) {
	store := newFakeStore()
	store.duplicate = true
	m := worker.New(fakeSource{ad: pendingAd(), found: true}, store, discardLog())

	require.NoError(t, m.Handle(context.Background(), adEnvelope(t, "ad1", "pending")))
	require.NotNil(t, store.manualTask)
	assert.Contains(t, store.manualTask.Reasons, domain.ReasonDuplicate)
}

func TestHandle_NonPendingEvent_Skips(t *testing.T) {
	store := newFakeStore()
	m := worker.New(fakeSource{found: true}, store, discardLog())

	require.NoError(t, m.Handle(context.Background(), adEnvelope(t, "ad1", "draft")))
	assert.Nil(t, store.approvedTask)
	assert.Nil(t, store.manualTask)
	assert.False(t, store.markedOnly)
}

func TestHandle_AdNotFound_MarksProcessed(t *testing.T) {
	store := newFakeStore()
	m := worker.New(fakeSource{found: false}, store, discardLog())

	require.NoError(t, m.Handle(context.Background(), adEnvelope(t, "ad1", "pending")))
	assert.True(t, store.markedOnly)
	assert.Nil(t, store.approvedTask)
	assert.Nil(t, store.manualTask)
}

func TestHandle_AdLeftPending_MarksProcessed(t *testing.T) {
	store := newFakeStore()
	ad := pendingAd()
	ad.Status = "active" // уже активировано к моменту обработки
	m := worker.New(fakeSource{ad: ad, found: true}, store, discardLog())

	require.NoError(t, m.Handle(context.Background(), adEnvelope(t, "ad1", "pending")))
	assert.True(t, store.markedOnly)
	assert.Nil(t, store.approvedTask)
}

func TestHandle_AlreadyProcessed_Skips(t *testing.T) {
	store := newFakeStore()
	env := adEnvelope(t, "ad1", "pending")
	store.processed[env.ID] = true
	m := worker.New(fakeSource{ad: pendingAd(), found: true}, store, discardLog())

	require.NoError(t, m.Handle(context.Background(), env))
	assert.Nil(t, store.approvedTask)
	assert.Nil(t, store.manualTask)
}

func TestHandle_EmptyAdID_Errors(t *testing.T) {
	store := newFakeStore()
	m := worker.New(fakeSource{found: true}, store, discardLog())
	assert.Error(t, m.Handle(context.Background(), adEnvelope(t, "", "pending")))
}
