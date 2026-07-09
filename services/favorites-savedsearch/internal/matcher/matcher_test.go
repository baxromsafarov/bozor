package matcher

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/favorites-savedsearch/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeSource struct {
	ad    domain.AdView
	found bool
	err   error
}

func (f *fakeSource) GetAd(context.Context, string) (domain.AdView, bool, error) {
	return f.ad, f.found, f.err
}

type recordCall struct {
	searchID string
	adID     string
}

type fakeStore struct {
	candidates []domain.SavedSearch
	records    []recordCall
	published  map[string]bool // ключ searchID|adID → публиковать
	err        error
}

func (f *fakeStore) Candidates(context.Context, string, int16) ([]domain.SavedSearch, error) {
	return f.candidates, f.err
}

func (f *fakeStore) RecordMatchWithEvent(_ context.Context, searchID, adID string, _ int, _ events.Envelope) (bool, error) {
	f.records = append(f.records, recordCall{searchID, adID})
	return f.published[searchID+"|"+adID], nil
}

func approvedEvent(t *testing.T, adID string) events.Envelope {
	t.Helper()
	ev, err := events.New(events.SubjectAdApproved, "moderation", map[string]string{"ad_id": adID})
	require.NoError(t, err)
	return ev
}

func adView() domain.AdView {
	return domain.AdView{ID: "ad-1", CategoryID: "cars", RegionID: 14, Price: 4000, Currency: "USD", Title: "BMW"}
}

func TestMatcher_Handle_MatchesAndRecords(t *testing.T) {
	src := &fakeSource{ad: adView(), found: true}
	store := &fakeStore{
		candidates: []domain.SavedSearch{
			{ID: "s1", UserID: "u1", Name: "cars", Query: domain.SearchQuery{CategoryID: "cars"}},     // совпадает
			{ID: "s2", UserID: "u2", Name: "phones", Query: domain.SearchQuery{CategoryID: "phones"}}, // не совпадает
			{ID: "s3", UserID: "u3", Name: "cheap", Query: domain.SearchQuery{PriceMax: ptr(1000)}},   // не совпадает (цена)
		},
		published: map[string]bool{"s1|ad-1": true},
	}
	err := New(src, store, time.Minute, discardLogger()).Handle(context.Background(), approvedEvent(t, "ad-1"))
	require.NoError(t, err)
	// Только совпавший кандидат записан.
	require.Len(t, store.records, 1)
	assert.Equal(t, "s1", store.records[0].searchID)
	assert.Equal(t, "ad-1", store.records[0].adID)
}

func TestMatcher_Handle_AdNotFound(t *testing.T) {
	store := &fakeStore{candidates: []domain.SavedSearch{{ID: "s1", Query: domain.SearchQuery{}}}}
	err := New(&fakeSource{found: false}, store, time.Minute, discardLogger()).
		Handle(context.Background(), approvedEvent(t, "ad-1"))
	require.NoError(t, err)
	assert.Empty(t, store.records, "объявление не найдено — совпадений не ищем")
}

func TestMatcher_Handle_EmptyAdID(t *testing.T) {
	err := New(&fakeSource{}, &fakeStore{}, time.Minute, discardLogger()).
		Handle(context.Background(), approvedEvent(t, ""))
	require.Error(t, err)
}

func TestMatcher_Handle_SourceError(t *testing.T) {
	src := &fakeSource{err: errors.New("listing down")}
	err := New(src, &fakeStore{}, time.Minute, discardLogger()).
		Handle(context.Background(), approvedEvent(t, "ad-1"))
	require.Error(t, err, "ошибка чтения объявления → повтор/DLQ")
}

func ptr(v int64) *int64 { return &v }
