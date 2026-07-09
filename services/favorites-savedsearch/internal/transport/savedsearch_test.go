package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/authx"

	"bozor/services/favorites-savedsearch/internal/app"
	"bozor/services/favorites-savedsearch/internal/domain"
)

// fakeSavedSvc — управляемая реализация transport.SavedSearchService.
type fakeSavedSvc struct {
	created  domain.SavedSearch
	list     []domain.SavedSearch
	err      error
	gotOwner string
	gotIn    app.CreateSavedSearchInput
	gotID    string
}

func (f *fakeSavedSvc) Create(_ context.Context, userID string, in app.CreateSavedSearchInput) (domain.SavedSearch, error) {
	f.gotOwner, f.gotIn = userID, in
	return f.created, f.err
}

func (f *fakeSavedSvc) List(_ context.Context, userID string) ([]domain.SavedSearch, error) {
	f.gotOwner = userID
	return f.list, f.err
}

func (f *fakeSavedSvc) Delete(_ context.Context, id, userID string) error {
	f.gotID, f.gotOwner = id, userID
	return f.err
}

func savedRouter(h *SavedSearchHandler, userID string) http.Handler {
	r := chi.NewRouter()
	r.Use(authx.FromForwardedHeaders)
	r.Post("/api/v1/saved-searches", h.Create)
	r.Get("/api/v1/me/saved-searches", h.List)
	r.Delete("/api/v1/saved-searches/{id}", h.Delete)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if userID != "" {
			req.Header.Set("X-User-Id", userID)
		}
		r.ServeHTTP(w, req)
	})
}

func TestSavedSearchHandler_Create(t *testing.T) {
	fs := &fakeSavedSvc{created: domain.SavedSearch{
		ID: "s1", Name: "Машины", Query: domain.SearchQuery{CategoryID: "cars", RegionID: 14},
		NotifyEnabled: true, CreatedAt: time.Unix(1700000000, 0).UTC(),
	}}
	h := NewSavedSearchHandler(fs)
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"Машины","query":{"category_id":"cars","region_id":14},"notify_enabled":true}`)
	savedRouter(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/saved-searches", body))

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp savedSearchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "s1", resp.ID)
	assert.Equal(t, "cars", resp.Query.CategoryID)
	assert.Equal(t, "u1", fs.gotOwner)
	assert.Equal(t, "Машины", fs.gotIn.Name)
	assert.True(t, fs.gotIn.NotifyEnabled)
}

func TestSavedSearchHandler_Create_DefaultNotifyEnabled(t *testing.T) {
	fs := &fakeSavedSvc{created: domain.SavedSearch{ID: "s1"}}
	h := NewSavedSearchHandler(fs)
	rec := httptest.NewRecorder()
	// notify_enabled опущен → по умолчанию true.
	body := strings.NewReader(`{"name":"Машины","query":{"category_id":"cars"}}`)
	savedRouter(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/saved-searches", body))
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.True(t, fs.gotIn.NotifyEnabled, "по умолчанию уведомления включены")
}

func TestSavedSearchHandler_Create_Unauthorized(t *testing.T) {
	h := NewSavedSearchHandler(&fakeSavedSvc{})
	rec := httptest.NewRecorder()
	savedRouter(h, "").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/saved-searches", strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSavedSearchHandler_Create_TooMany(t *testing.T) {
	fs := &fakeSavedSvc{err: domain.ErrTooManySavedSearches}
	h := NewSavedSearchHandler(fs)
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"x","query":{}}`)
	savedRouter(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/saved-searches", body))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "too_many_saved_searches")
}

func TestSavedSearchHandler_List(t *testing.T) {
	fs := &fakeSavedSvc{list: []domain.SavedSearch{{ID: "s1", Name: "Машины"}}}
	h := NewSavedSearchHandler(fs)
	rec := httptest.NewRecorder()
	savedRouter(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me/saved-searches", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp savedSearchListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.SavedSearches, 1)
	assert.Equal(t, "s1", resp.SavedSearches[0].ID)
}

func TestSavedSearchHandler_Delete(t *testing.T) {
	fs := &fakeSavedSvc{}
	h := NewSavedSearchHandler(fs)
	rec := httptest.NewRecorder()
	savedRouter(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/saved-searches/s1", nil))
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "s1", fs.gotID)
}

func TestSavedSearchHandler_Delete_NotFound(t *testing.T) {
	fs := &fakeSavedSvc{err: domain.ErrSavedSearchNotFound}
	h := NewSavedSearchHandler(fs)
	rec := httptest.NewRecorder()
	savedRouter(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/saved-searches/s1", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "saved_search_not_found")
}
