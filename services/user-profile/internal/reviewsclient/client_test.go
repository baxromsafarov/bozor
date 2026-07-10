package reviewsclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRating_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/users/seller-1/rating", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"seller-1","avg_rating":4.5,"reviews_count":2}`))
	}))
	defer srv.Close()

	rt, err := New(srv.URL, time.Second).GetRating(context.Background(), "seller-1")
	require.NoError(t, err)
	assert.Equal(t, 4.5, rt.AvgRating)
	assert.Equal(t, 2, rt.ReviewsCount)
}

func TestGetRating_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := New(srv.URL, time.Second).GetRating(context.Background(), "seller-1")
	require.Error(t, err)
}
