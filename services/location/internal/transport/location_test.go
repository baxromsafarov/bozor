package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/location/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func fptr(f float64) *float64 { return &f }

// fakeService подменяет use-cases справочника.
type fakeService struct {
	regions   []domain.Region
	regionErr error
	cities    []domain.City
	citiesErr error
}

func (f *fakeService) Regions(context.Context) ([]domain.Region, error) {
	return f.regions, f.regionErr
}

func (f *fakeService) Cities(context.Context, int) ([]domain.City, error) {
	return f.cities, f.citiesErr
}

func newRouter(svc Service) http.Handler {
	return NewRouter(Deps{Log: discardLogger(), Handler: NewHandler(svc, discardLogger())})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRegions_OK(t *testing.T) {
	svc := &fakeService{regions: []domain.Region{
		{ID: 1, Slug: "toshkent-shahri", NameUZ: "Toshkent shahri", NameRU: "город Ташкент", Latitude: fptr(41.31), Longitude: fptr(69.28)},
	}}
	rec := get(t, newRouter(svc), "/api/v1/locations/regions")

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Regions []regionResponse `json:"regions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Regions, 1)
	assert.Equal(t, "toshkent-shahri", body.Regions[0].Slug)
	assert.Equal(t, "город Ташкент", body.Regions[0].NameRU)
	require.NotNil(t, body.Regions[0].Latitude)
	assert.InDelta(t, 41.31, *body.Regions[0].Latitude, 0.001)
}

func TestCities_OK(t *testing.T) {
	svc := &fakeService{cities: []domain.City{
		{ID: 101, RegionID: 1, Slug: "chilonzor", NameUZ: "Chilonzor", NameRU: "Чиланзар"},
	}}
	rec := get(t, newRouter(svc), "/api/v1/locations/regions/1/cities")

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Cities []cityResponse `json:"cities"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Cities, 1)
	assert.Equal(t, "chilonzor", body.Cities[0].Slug)
	assert.Equal(t, 1, body.Cities[0].RegionID)
}

func TestCities_NotFound(t *testing.T) {
	svc := &fakeService{citiesErr: domain.ErrRegionNotFound}
	rec := get(t, newRouter(svc), "/api/v1/locations/regions/999/cities")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	assert.Contains(t, rec.Body.String(), "region_not_found")
}

func TestCities_InvalidID(t *testing.T) {
	rec := get(t, newRouter(&fakeService{}), "/api/v1/locations/regions/abc/cities")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_region_id")
}

func TestRouter_NotFound(t *testing.T) {
	rec := get(t, newRouter(&fakeService{}), "/api/v1/locations/unknown")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
}
