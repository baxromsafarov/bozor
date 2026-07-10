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

	promos     []domain.Ad
	promosErr  error
	clearOK    map[string]bool
	cleared    []string
	clearedEvs []events.Envelope
}

func (f *fakeExpiryStore) ListExpired(context.Context, time.Time, int) ([]domain.Ad, error) {
	return f.list, f.listErr
}

func (f *fakeExpiryStore) ExpireWithEvent(_ context.Context, adID string, ev events.Envelope) (bool, error) {
	f.expired = append(f.expired, adID)
	f.events = append(f.events, ev)
	return f.expireOK[adID], nil
}

func (f *fakeExpiryStore) ListExpiredPromos(context.Context, time.Time, int) ([]domain.Ad, error) {
	return f.promos, f.promosErr
}

func (f *fakeExpiryStore) ClearPromotionWithEvent(_ context.Context, adID string, ev events.Envelope) (bool, error) {
	f.cleared = append(f.cleared, adID)
	f.clearedEvs = append(f.clearedEvs, ev)
	return f.clearOK[adID], nil
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

func TestExpirer_SweepClearsExpiredPromos(t *testing.T) {
	p1 := domain.Ad{ID: "p1", UserID: "u", CategoryID: "c", Title: "x", Currency: "UZS",
		RegionID: 1, Status: domain.StatusActive, IsTop: true, PromotionRank: 42}
	p2 := domain.Ad{ID: "p2", UserID: "u", CategoryID: "c", Title: "y", Currency: "UZS",
		RegionID: 1, Status: domain.StatusActive, IsTop: true, PromotionRank: 7}
	store := &fakeExpiryStore{promos: []domain.Ad{p1, p2}, clearOK: map[string]bool{"p1": true, "p2": false}}

	NewExpirer(store, time.Minute, 100, discardLogger()).Sweep(context.Background())

	assert.Equal(t, []string{"p1", "p2"}, store.cleared, "оба кандидата на снятие TOP обработаны")
	require.Len(t, store.clearedEvs, 2)
	assert.Equal(t, events.SubjectAdUpdated, store.clearedEvs[0].Type, "снятие публикует bozor.ad.updated")
}

func TestExpirer_SweepPromosError(t *testing.T) {
	store := &fakeExpiryStore{promosErr: assertErr}
	NewExpirer(store, time.Minute, 100, discardLogger()).Sweep(context.Background())
	assert.Empty(t, store.cleared, "ошибка выборки промо — ничего не снимается")
}

// --- Promoter ---

type fakePromoStore struct {
	ad        domain.Ad
	getErr    error
	appliedID string
	appliedRk int32
	appliedEv events.Envelope
	applyOK   bool
	calls     int
}

func (f *fakePromoStore) GetByID(context.Context, string) (domain.Ad, error) {
	if f.getErr != nil {
		return domain.Ad{}, f.getErr
	}
	return f.ad, nil
}

func (f *fakePromoStore) SetPromotionWithEvent(_ context.Context, adID string, rank int32, _ *time.Time, ev events.Envelope) (bool, error) {
	f.calls++
	f.appliedID = adID
	f.appliedRk = rank
	f.appliedEv = ev
	return f.applyOK, nil
}

func promoEnv(t *testing.T, adID string, isTop bool, rank int64, endsAt *time.Time) events.Envelope {
	t.Helper()
	e, err := events.New(events.SubjectPromotionActivated, "payments", map[string]any{
		"ad_id": adID, "is_top": isTop, "promotion_rank": rank, "ends_at": endsAt,
	})
	require.NoError(t, err)
	return e
}

func TestPromoter_AppliesTop(t *testing.T) {
	ends := time.Now().Add(7 * 24 * time.Hour).UTC()
	store := &fakePromoStore{ad: pendingActiveAd(), applyOK: true}
	err := NewPromoter(store, discardLogger()).
		Handle(context.Background(), promoEnv(t, "ad-1", true, ends.Unix(), &ends))
	require.NoError(t, err)
	assert.Equal(t, "ad-1", store.appliedID, "промо применено к объявлению")
	assert.Equal(t, int32(ends.Unix()), store.appliedRk, "promotion_rank из события")
	assert.Equal(t, events.SubjectAdUpdated, store.appliedEv.Type, "публикуется bozor.ad.updated")

	var payload app.AdEvent
	require.NoError(t, store.appliedEv.Decode(&payload))
	assert.Equal(t, "active", payload.Status)
}

func TestPromoter_NonTopIsNoop(t *testing.T) {
	store := &fakePromoStore{ad: pendingActiveAd()}
	err := NewPromoter(store, discardLogger()).
		Handle(context.Background(), promoEnv(t, "ad-1", false, 0, nil))
	require.NoError(t, err)
	assert.Zero(t, store.calls, "не-TOP активация не трогает объявление")
}

func TestPromoter_AdMissingIsNoop(t *testing.T) {
	store := &fakePromoStore{getErr: domain.ErrAdNotFound}
	err := NewPromoter(store, discardLogger()).
		Handle(context.Background(), promoEnv(t, "ad-1", true, 1, nil))
	require.NoError(t, err, "удалённое объявление — подтвердить без ошибки")
	assert.Zero(t, store.calls)
}

func TestPromoter_EmptyAdIDErrors(t *testing.T) {
	store := &fakePromoStore{ad: pendingActiveAd()}
	err := NewPromoter(store, discardLogger()).
		Handle(context.Background(), promoEnv(t, "", true, 1, nil))
	require.Error(t, err)
	assert.Zero(t, store.calls)
}

func pendingActiveAd() domain.Ad {
	a := pendingAd()
	a.Status = domain.StatusActive
	return a
}

var assertErr = errStub("boom")

type errStub string

func (e errStub) Error() string { return string(e) }
