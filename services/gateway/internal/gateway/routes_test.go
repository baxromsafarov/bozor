package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/gateway/internal/ratelimit"
)

// newMultiUpstreamRouter собирает роутер, резолвящий upstream по имени сервиса.
func newMultiUpstreamRouter(t *testing.T, byService map[string]string) http.Handler {
	t.Helper()
	h, err := NewRouter(Deps{
		Log:            discardLogger(),
		JWTKey:         testKey,
		Limiter:        fakeLimiter{res: ratelimit.Result{Allowed: true, Remaining: 39}},
		RateRPS:        20,
		RateBurst:      40,
		AllowedOrigins: []string{"*"},
		Upstream:       func(s string) string { return byService[s] },
	})
	require.NoError(t, err)
	return h
}

// TestRoutes_AdsSearchPrecedence проверяет, что более специфичный /api/v1/ads/search
// уходит в search, а /api/v1/ads и /api/v1/ads/{id} — в listing-ads.
func TestRoutes_AdsSearchPrecedence(t *testing.T) {
	search := newEchoBackend()
	defer search.server.Close()
	listing := newEchoBackend()
	defer listing.server.Close()

	router := newMultiUpstreamRouter(t, map[string]string{
		"search":      search.server.URL,
		"listing-ads": listing.server.URL,
	})

	cases := []struct {
		path        string
		wantSearch  int64
		wantListing int64
	}{
		{"/api/v1/ads/search?q=bmw", 1, 0},
		{"/api/v1/ads/search/facets?category_id=cars", 1, 0},
		{"/api/v1/ads", 0, 1},
		{"/api/v1/ads/42", 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			search.calls.Store(0)
			listing.calls.Store(0)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tc.wantSearch, search.calls.Load(), "вызовы search")
			assert.Equal(t, tc.wantListing, listing.calls.Load(), "вызовы listing")
		})
	}
}
