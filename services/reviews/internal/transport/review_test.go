package transport

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/reviews/internal/app"
	"bozor/services/reviews/internal/domain"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	createErr error
	list      []domain.Review
}

func (f *fakeStore) CreateWithEvent(context.Context, domain.Review, events.Envelope) error {
	return f.createErr
}
func (f *fakeStore) ListByTarget(context.Context, string, int, int) ([]domain.Review, error) {
	return f.list, nil
}

type fakeAds struct {
	ad    domain.AdView
	found bool
}

func (f *fakeAds) GetAd(context.Context, string) (domain.AdView, bool, error) {
	return f.ad, f.found, nil
}

func server(store *fakeStore, ads *fakeAds, userID string) http.Handler {
	svc := app.NewService(store, ads, discardLog())
	router := NewRouter(Deps{Log: discardLog(), Reviews: NewReviewHandler(svc, discardLog())})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userID != "" {
			r.Header.Set("X-User-Id", userID)
		}
		router.ServeHTTP(w, r)
	})
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sellerAd() *fakeAds {
	return &fakeAds{ad: domain.AdView{ID: "ad-1", UserID: "seller-1", Status: "active"}, found: true}
}

func TestCreate_201(t *testing.T) {
	rec := do(t, server(&fakeStore{}, sellerAd(), "buyer-1"),
		http.MethodPost, "/api/v1/reviews", `{"ad_id":"ad-1","rating":5,"body":"отлично"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), `"target_id":"seller-1"`)
	assert.Contains(t, rec.Body.String(), `"rating":5`)
}

func TestCreate_Anonymous_401(t *testing.T) {
	rec := do(t, server(&fakeStore{}, sellerAd(), ""),
		http.MethodPost, "/api/v1/reviews", `{"ad_id":"ad-1","rating":5}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreate_InvalidRating_422(t *testing.T) {
	rec := do(t, server(&fakeStore{}, sellerAd(), "buyer-1"),
		http.MethodPost, "/api/v1/reviews", `{"ad_id":"ad-1","rating":9}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestCreate_SelfReview_409(t *testing.T) {
	rec := do(t, server(&fakeStore{}, sellerAd(), "seller-1"),
		http.MethodPost, "/api/v1/reviews", `{"ad_id":"ad-1","rating":4}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "self_review")
}

func TestCreate_Duplicate_409(t *testing.T) {
	rec := do(t, server(&fakeStore{createErr: domain.ErrDuplicateReview}, sellerAd(), "buyer-1"),
		http.MethodPost, "/api/v1/reviews", `{"ad_id":"ad-1","rating":5}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "duplicate_review")
}

func TestCreate_AdNotFound_404(t *testing.T) {
	rec := do(t, server(&fakeStore{}, &fakeAds{found: false}, "buyer-1"),
		http.MethodPost, "/api/v1/reviews", `{"ad_id":"ad-x","rating":5}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListByUser_200(t *testing.T) {
	store := &fakeStore{list: []domain.Review{
		{ID: "r1", TargetID: "seller-1", Rating: 5, Body: "супер", Status: "active"},
	}}
	rec := do(t, server(store, &fakeAds{}, ""), http.MethodGet, "/api/v1/users/seller-1/reviews", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"reviews"`)
	assert.Contains(t, rec.Body.String(), `"r1"`)
}
