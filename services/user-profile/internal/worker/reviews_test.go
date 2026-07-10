package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/user-profile/internal/domain"
)

type fakeRatingStore struct {
	processed map[string]bool
	upserts   []ratingUpsert
}

type ratingUpsert struct {
	userID string
	rating domain.Rating
}

func newFakeRatingStore() *fakeRatingStore { return &fakeRatingStore{processed: map[string]bool{}} }

func (f *fakeRatingStore) IsEventProcessed(_ context.Context, consumer, eventID string) (bool, error) {
	return f.processed[consumer+"|"+eventID], nil
}

func (f *fakeRatingStore) UpsertRatingWithInbox(_ context.Context, def domain.Profile, rt domain.Rating, consumer, eventID string) error {
	f.processed[consumer+"|"+eventID] = true
	f.upserts = append(f.upserts, ratingUpsert{userID: def.UserID, rating: rt})
	return nil
}

type fakeRatings struct {
	rating domain.Rating
	err    error
	calls  int
}

func (f *fakeRatings) GetRating(_ context.Context, _ string) (domain.Rating, error) {
	f.calls++
	return f.rating, f.err
}

func reviewCreatedEvent(t *testing.T, targetID string) events.Envelope {
	t.Helper()
	ev, err := events.New(events.SubjectReviewCreated, "reviews", map[string]any{
		"user_id": targetID, "target_id": targetID, "rating": 5, "review_id": "rev-1",
	})
	require.NoError(t, err)
	return ev
}

func TestReviews_Handle_UpdatesRating(t *testing.T) {
	store := newFakeRatingStore()
	ratings := &fakeRatings{rating: domain.Rating{AvgRating: 4.5, ReviewsCount: 2}}
	err := NewReviews(store, ratings, discardLogger()).
		Handle(context.Background(), reviewCreatedEvent(t, "seller-1"))
	require.NoError(t, err)
	require.Len(t, store.upserts, 1)
	assert.Equal(t, "seller-1", store.upserts[0].userID, "рейтинг адресата (продавца)")
	assert.Equal(t, 4.5, store.upserts[0].rating.AvgRating, "агрегат перечитан из Reviews, не из события")
	assert.Equal(t, 2, store.upserts[0].rating.ReviewsCount)
	assert.Equal(t, 1, ratings.calls)
}

func TestReviews_Handle_IdempotentSkip(t *testing.T) {
	store := newFakeRatingStore()
	ratings := &fakeRatings{rating: domain.Rating{AvgRating: 5, ReviewsCount: 1}}
	c := NewReviews(store, ratings, discardLogger())
	ev := reviewCreatedEvent(t, "seller-1")

	require.NoError(t, c.Handle(context.Background(), ev))
	require.NoError(t, c.Handle(context.Background(), ev), "повтор того же события")
	assert.Len(t, store.upserts, 1, "рейтинг пересчитан один раз")
	assert.Equal(t, 1, ratings.calls, "Reviews не опрашивается повторно на обработанном событии")
}

func TestReviews_Handle_EmptyTarget(t *testing.T) {
	store := newFakeRatingStore()
	err := NewReviews(store, &fakeRatings{}, discardLogger()).
		Handle(context.Background(), reviewCreatedEvent(t, ""))
	require.Error(t, err)
	assert.Empty(t, store.upserts)
}

func TestReviews_Handle_ReviewsUnavailable(t *testing.T) {
	store := newFakeRatingStore()
	ratings := &fakeRatings{err: errors.New("reviews down")}
	err := NewReviews(store, ratings, discardLogger()).
		Handle(context.Background(), reviewCreatedEvent(t, "seller-1"))
	require.Error(t, err, "недоступность Reviews → повтор")
	assert.Empty(t, store.upserts, "кеш не трогаем без актуального агрегата")
}
