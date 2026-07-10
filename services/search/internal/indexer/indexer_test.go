package indexer

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/search/internal/listingclient"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeDocs struct {
	upserted []adDoc
	deleted  []string
}

func (f *fakeDocs) UpsertDocument(_ context.Context, _ string, doc any) error {
	f.upserted = append(f.upserted, doc.(adDoc))
	return nil
}

func (f *fakeDocs) DeleteDocument(_ context.Context, _, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

type fakeSource struct {
	ad    listingclient.Ad
	found bool
	err   error
	pages [][]listingclient.Ad
}

func (f *fakeSource) GetAd(context.Context, string) (listingclient.Ad, bool, error) {
	return f.ad, f.found, f.err
}

func (f *fakeSource) ListActive(_ context.Context, after string, _ int) ([]listingclient.Ad, string, error) {
	if len(f.pages) == 0 {
		return nil, "", nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	next := ""
	if len(f.pages) > 0 && len(page) > 0 {
		next = page[len(page)-1].ID
	}
	return page, next, nil
}

func activeAd() listingclient.Ad {
	lat, lng := 41.3, 69.2
	return listingclient.Ad{
		ID: "ad-1", CategoryID: "cat", Title: "BMW X5", Description: "хорошее",
		Price: 500, Currency: "UZS", RegionID: 1, Status: "active",
		Attributes: []listingclient.Attr{{Slug: "brand", Value: "bmw"}},
		CreatedAt:  "2026-07-09T10:00:00Z", Lat: &lat, Lng: &lng,
	}
}

func evt(t *testing.T, subject, adID string) events.Envelope {
	t.Helper()
	e, err := events.New(subject, "listing", map[string]string{"ad_id": adID})
	require.NoError(t, err)
	return e
}

func TestHandle_ActiveUpserts(t *testing.T) {
	docs := &fakeDocs{}
	src := &fakeSource{ad: activeAd(), found: true}
	err := New(docs, src, discardLogger()).Handle(context.Background(), evt(t, events.SubjectAdUpdated, "ad-1"))
	require.NoError(t, err)
	require.Len(t, docs.upserted, 1)
	d := docs.upserted[0]
	assert.Equal(t, "ad-1", d.ID)
	assert.Equal(t, "bmw", d.Attrs["brand"], "атрибуты в attrs.<slug>")
	assert.Equal(t, []float64{41.3, 69.2}, d.Location, "гео из lat/lng")
	wantCreated, _ := time.Parse(time.RFC3339, "2026-07-09T10:00:00Z")
	assert.Equal(t, wantCreated.Unix(), d.CreatedAt, "created_at в unix")
	assert.Empty(t, docs.deleted)
}

func TestHandle_PromoFieldsMapped(t *testing.T) {
	docs := &fakeDocs{}
	ad := activeAd()
	ad.IsTop = true
	ad.PromotionRank = 1783641600
	ad.PromoEndsAt = "2026-08-08T10:00:00Z"
	src := &fakeSource{ad: ad, found: true}
	err := New(docs, src, discardLogger()).Handle(context.Background(), evt(t, events.SubjectAdUpdated, "ad-1"))
	require.NoError(t, err)
	require.Len(t, docs.upserted, 1)
	d := docs.upserted[0]
	assert.True(t, d.IsTop, "промо-флаг в индексе")
	assert.Equal(t, int32(1783641600), d.PromotionRank)
	wantEnds, _ := time.Parse(time.RFC3339, "2026-08-08T10:00:00Z")
	require.NotNil(t, d.PromoEndsAt)
	assert.Equal(t, wantEnds.Unix(), *d.PromoEndsAt, "promo_ends_at в unix")
}

func TestHandle_NonActiveDeletes(t *testing.T) {
	docs := &fakeDocs{}
	ad := activeAd()
	ad.Status = "sold"
	src := &fakeSource{ad: ad, found: true}
	err := New(docs, src, discardLogger()).Handle(context.Background(), evt(t, events.SubjectAdSold, "ad-1"))
	require.NoError(t, err)
	assert.Equal(t, []string{"ad-1"}, docs.deleted, "не активное — снято из индекса")
	assert.Empty(t, docs.upserted)
}

func TestHandle_NotFoundDeletes(t *testing.T) {
	docs := &fakeDocs{}
	src := &fakeSource{found: false} // удалено в Listing
	err := New(docs, src, discardLogger()).Handle(context.Background(), evt(t, events.SubjectAdDeleted, "ad-1"))
	require.NoError(t, err)
	assert.Equal(t, []string{"ad-1"}, docs.deleted)
}

func TestHandle_EmptyAdIDErrors(t *testing.T) {
	err := New(&fakeDocs{}, &fakeSource{}, discardLogger()).Handle(context.Background(), evt(t, events.SubjectAdUpdated, ""))
	require.Error(t, err)
}

func TestReindex_PagesAndUpserts(t *testing.T) {
	docs := &fakeDocs{}
	a1, a2, a3 := activeAd(), activeAd(), activeAd()
	a2.ID, a3.ID = "ad-2", "ad-3"
	src := &fakeSource{pages: [][]listingclient.Ad{{a1, a2}, {a3}}}
	n, err := New(docs, src, discardLogger()).Reindex(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Len(t, docs.upserted, 3)
}
