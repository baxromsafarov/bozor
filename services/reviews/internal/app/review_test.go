package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/reviews/internal/domain"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	created   *domain.Review
	ev        events.Envelope
	createErr error
	list      []domain.Review
}

func (f *fakeStore) CreateWithEvent(_ context.Context, rev domain.Review, ev events.Envelope) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = &rev
	f.ev = ev
	return nil
}

func (f *fakeStore) ListByTarget(_ context.Context, _ string, _, _ int) ([]domain.Review, error) {
	return f.list, nil
}

type fakeAds struct {
	ad    domain.AdView
	found bool
	err   error
}

func (f *fakeAds) GetAd(context.Context, string) (domain.AdView, bool, error) {
	return f.ad, f.found, f.err
}

func TestCreate_Success(t *testing.T) {
	store := &fakeStore{}
	ads := &fakeAds{ad: domain.AdView{ID: "ad-1", UserID: "seller-1", Status: "active"}, found: true}
	rev, err := NewService(store, ads, discardLog()).Create(context.Background(), CreateInput{
		AuthorID: "buyer-1", AdID: "ad-1", Rating: 5, Body: "  отлично  ",
	})
	require.NoError(t, err)
	assert.Equal(t, "seller-1", rev.TargetID, "адресат = владелец объявления из Listing")
	assert.Equal(t, "buyer-1", rev.AuthorID)
	assert.Equal(t, 5, rev.Rating)
	assert.Equal(t, "отлично", rev.Body, "текст нормализован")
	assert.Equal(t, domain.StatusActive, rev.Status)
	require.NotNil(t, store.created)
	assert.Equal(t, events.SubjectReviewCreated, store.ev.Type)

	var payload ReviewEvent
	require.NoError(t, store.ev.Decode(&payload))
	assert.Equal(t, "seller-1", payload.TargetID)
	assert.Equal(t, 5, payload.Rating)
}

func TestCreate_SelfReviewRejected(t *testing.T) {
	store := &fakeStore{}
	ads := &fakeAds{ad: domain.AdView{ID: "ad-1", UserID: "u1", Status: "active"}, found: true}
	_, err := NewService(store, ads, discardLog()).Create(context.Background(), CreateInput{
		AuthorID: "u1", AdID: "ad-1", Rating: 4,
	})
	assert.ErrorIs(t, err, domain.ErrSelfReview)
	assert.Nil(t, store.created, "отзыв о своём объявлении не создаётся")
}

func TestCreate_AdNotFound(t *testing.T) {
	store := &fakeStore{}
	ads := &fakeAds{found: false}
	_, err := NewService(store, ads, discardLog()).Create(context.Background(), CreateInput{
		AuthorID: "buyer-1", AdID: "ad-x", Rating: 4,
	})
	assert.ErrorIs(t, err, domain.ErrAdNotFound)
	assert.Nil(t, store.created)
}

func TestCreate_InvalidRating(t *testing.T) {
	store := &fakeStore{}
	ads := &fakeAds{ad: domain.AdView{UserID: "seller-1"}, found: true}
	_, err := NewService(store, ads, discardLog()).Create(context.Background(), CreateInput{
		AuthorID: "buyer-1", AdID: "ad-1", Rating: 7,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidRating)
	assert.Nil(t, store.created, "невалидная оценка — до Listing не доходит")
}

func TestCreate_DuplicatePropagated(t *testing.T) {
	store := &fakeStore{createErr: domain.ErrDuplicateReview}
	ads := &fakeAds{ad: domain.AdView{ID: "ad-1", UserID: "seller-1", Status: "active"}, found: true}
	_, err := NewService(store, ads, discardLog()).Create(context.Background(), CreateInput{
		AuthorID: "buyer-1", AdID: "ad-1", Rating: 5,
	})
	assert.ErrorIs(t, err, domain.ErrDuplicateReview)
}

func TestCreate_ListingErrorPropagated(t *testing.T) {
	store := &fakeStore{}
	ads := &fakeAds{err: errors.New("listing down")}
	_, err := NewService(store, ads, discardLog()).Create(context.Background(), CreateInput{
		AuthorID: "buyer-1", AdID: "ad-1", Rating: 5,
	})
	require.Error(t, err)
	assert.Nil(t, store.created)
}

func TestListByUser_ClampsAndReturns(t *testing.T) {
	store := &fakeStore{list: []domain.Review{{ID: "r1"}, {ID: "r2"}}}
	got, err := NewService(store, &fakeAds{}, discardLog()).ListByUser(context.Background(), "seller-1", 0, -5)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}
