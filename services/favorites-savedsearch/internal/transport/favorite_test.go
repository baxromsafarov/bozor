package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/authx"

	"bozor/services/favorites-savedsearch/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const adID = "019f4780-2222-7abc-8def-000000000002"

// fakeService — управляемая реализация transport.Service.
type fakeService struct {
	fav      domain.Favorite
	list     []domain.Favorite
	err      error
	gotOwner string
	gotAd    string
}

func (f *fakeService) Add(_ context.Context, userID, ad string) (domain.Favorite, error) {
	f.gotOwner, f.gotAd = userID, ad
	return f.fav, f.err
}

func (f *fakeService) Remove(_ context.Context, userID, ad string) error {
	f.gotOwner, f.gotAd = userID, ad
	return f.err
}

func (f *fakeService) List(_ context.Context, userID string, _, _ int) ([]domain.Favorite, error) {
	f.gotOwner = userID
	return f.list, f.err
}

// routerWith собирает мини-роутер с идентичностью из заголовка X-User-Id.
func routerWith(h *Handler, userID string) http.Handler {
	r := chi.NewRouter()
	r.Use(authx.FromForwardedHeaders)
	r.Post("/api/v1/favorites/{adId}", h.Add)
	r.Delete("/api/v1/favorites/{adId}", h.Remove)
	r.Get("/api/v1/me/favorites", h.List)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if userID != "" {
			req.Header.Set("X-User-Id", userID)
		}
		r.ServeHTTP(w, req)
	})
}

func TestHandler_Add(t *testing.T) {
	fs := &fakeService{fav: domain.Favorite{AdID: adID, CreatedAt: time.Unix(1700000000, 0).UTC()}}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	routerWith(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/favorites/"+adID, nil))

	require.Equal(t, http.StatusCreated, rec.Code)
	var body favoriteDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, adID, body.AdID)
	assert.Equal(t, "u1", fs.gotOwner)
	assert.Equal(t, adID, fs.gotAd)
}

func TestHandler_Add_Unauthorized(t *testing.T) {
	h := NewHandler(&fakeService{}, discardLogger())
	rec := httptest.NewRecorder()
	routerWith(h, "").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/favorites/"+adID, nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_Add_InvalidAdID(t *testing.T) {
	fs := &fakeService{err: domain.ErrInvalidAdID}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	routerWith(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/favorites/bad", nil))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_ad_id")
}

func TestHandler_Remove(t *testing.T) {
	fs := &fakeService{}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	routerWith(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/favorites/"+adID, nil))
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, adID, fs.gotAd)
}

func TestHandler_List(t *testing.T) {
	fs := &fakeService{list: []domain.Favorite{
		{AdID: adID, CreatedAt: time.Unix(1700000000, 0).UTC()},
	}}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	routerWith(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me/favorites", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body favoritesListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Favorites, 1)
	assert.Equal(t, adID, body.Favorites[0].AdID)
	assert.Equal(t, "u1", fs.gotOwner)
}

func TestHandler_List_Unauthorized(t *testing.T) {
	h := NewHandler(&fakeService{}, discardLogger())
	rec := httptest.NewRecorder()
	routerWith(h, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me/favorites", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
