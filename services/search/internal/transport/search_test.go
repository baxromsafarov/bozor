package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/search/internal/app"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeSearcher фиксирует полученный запрос и возвращает заготовленный результат.
type fakeSearcher struct {
	gotQuery app.Query
	res      app.Result
	facets   []app.Facet
	err      error
}

func (f *fakeSearcher) Search(_ context.Context, q app.Query) (app.Result, error) {
	f.gotQuery = q
	return f.res, f.err
}

func (f *fakeSearcher) Facets(_ context.Context, q app.Query) ([]app.Facet, error) {
	f.gotQuery = q
	return f.facets, f.err
}

func TestParseQuery_AllParams(t *testing.T) {
	q, _ := url.ParseQuery("q=bmw&category_id=cars&region_id=14&city_id=101&currency=USD" +
		"&price_min=1000&price_max=5000&sort=price_asc&page=2&per_page=30" +
		"&attr.brand=bmw&attr.color=black&facets=brand,color&lat=41.3&lng=69.2&radius_km=5")
	got := parseQuery(q)

	assert.Equal(t, "bmw", got.Text)
	assert.Equal(t, "cars", got.CategoryID)
	assert.Equal(t, 14, got.RegionID)
	assert.Equal(t, int64(101), got.CityID)
	assert.Equal(t, "USD", got.Currency)
	require.NotNil(t, got.PriceMin)
	assert.Equal(t, int64(1000), *got.PriceMin)
	require.NotNil(t, got.PriceMax)
	assert.Equal(t, int64(5000), *got.PriceMax)
	assert.Equal(t, app.SortPriceAsc, got.Sort)
	assert.Equal(t, 2, got.Page)
	assert.Equal(t, 30, got.PerPage)
	assert.Equal(t, map[string]string{"brand": "bmw", "color": "black"}, got.Attrs)
	assert.Equal(t, []string{"brand", "color"}, got.AttrFacets)
	require.NotNil(t, got.Geo)
	assert.Equal(t, 41.3, got.Geo.Lat)
	assert.Equal(t, 69.2, got.Geo.Lng)
	assert.Equal(t, 5.0, got.Geo.RadiusKm)
}

func TestParseQuery_LenientOnGarbage(t *testing.T) {
	q, _ := url.ParseQuery("region_id=abc&price_min=xx&page=-&lat=41.3") // lng отсутствует
	got := parseQuery(q)
	assert.Equal(t, 0, got.RegionID, "мусорный region_id → 0")
	assert.Nil(t, got.PriceMin, "мусорный price_min → nil")
	assert.Nil(t, got.Geo, "неполная пара координат → без гео")
}

func TestSearchHandler_Search(t *testing.T) {
	fs := &fakeSearcher{res: app.Result{
		Found: 1, Page: 1, PerPage: 20,
		Hits: []app.AdHit{{ID: "ad-1", Title: "BMW", CategoryID: "cars", Price: 4000, Currency: "USD"}},
		Top:  []app.AdHit{{ID: "top-1", Title: "Featured", IsTop: true}},
	}}
	h := NewSearchHandler(fs, discardLogger())

	rec := httptest.NewRecorder()
	h.Search(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ads/search?q=bmw", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body searchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Found)
	require.Len(t, body.Hits, 1)
	assert.Equal(t, "ad-1", body.Hits[0].ID)
	require.Len(t, body.Top, 1, "топ-блок в ответе")
	assert.Equal(t, "top-1", body.Top[0].ID)
	assert.True(t, body.Top[0].IsTop)
	assert.Equal(t, "bmw", fs.gotQuery.Text)
}

func TestSearchHandler_Facets(t *testing.T) {
	fs := &fakeSearcher{facets: []app.Facet{
		{Field: "category_id", Values: []app.FacetValue{{Value: "cars", Count: 3}}},
	}}
	h := NewSearchHandler(fs, discardLogger())

	rec := httptest.NewRecorder()
	h.Facets(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ads/search/facets?category_id=cars", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body facetsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Facets, 1)
	assert.Equal(t, "category_id", body.Facets[0].Field)
	assert.Equal(t, "cars", body.Facets[0].Values[0].Value)
}

func TestSearchHandler_ErrorIsUnavailable(t *testing.T) {
	fs := &fakeSearcher{err: assert.AnError}
	h := NewSearchHandler(fs, discardLogger())

	rec := httptest.NewRecorder()
	h.Search(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ads/search?q=x", nil))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "search_unavailable")
}
