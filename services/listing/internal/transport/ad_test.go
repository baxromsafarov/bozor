package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/listing/internal/app"
	"bozor/services/listing/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeService struct {
	gotInput  app.CreateInput
	createErr error
	getResult domain.Ad
	getErr    error
}

func (f *fakeService) Create(_ context.Context, in app.CreateInput) (domain.Ad, error) {
	f.gotInput = in
	if f.createErr != nil {
		return domain.Ad{}, f.createErr
	}
	return domain.Ad{
		ID: "ad-1", UserID: in.UserID, CategoryID: in.CategoryID, Title: in.Title,
		Price: in.Price, Currency: "UZS", RegionID: in.RegionID, Status: domain.StatusDraft,
		Attributes: in.Attributes, Images: in.Images, CreatedAt: time.Unix(0, 0).UTC(),
	}, nil
}

func (f *fakeService) Get(_ context.Context, _ string) (domain.Ad, error) {
	if f.getErr != nil {
		return domain.Ad{}, f.getErr
	}
	return f.getResult, nil
}

func newRouter(svc Service) http.Handler {
	return NewRouter(Deps{Log: discardLogger(), Handler: NewHandler(svc, discardLogger())})
}

func do(t *testing.T, h http.Handler, method, path, owner, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if owner != "" {
		req.Header.Set("X-User-Id", owner)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const validBody = `{"category_id":"cat-cars","title":"BMW X5","price":500000000,"region_id":1,
	"attributes":[{"slug":"brand","value":"bmw"}],"images":[{"media_id":"m1","is_cover":true}]}`

func TestCreate_AnonUnauthorized(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads", "", validBody)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "unauthorized")
	assert.Empty(t, svc.gotInput.UserID, "аноним не доходит до use-case")
}

func TestCreate_HappyPath(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads", "user-1", validBody)
	require.Equal(t, http.StatusCreated, rec.Code)

	assert.Equal(t, "user-1", svc.gotInput.UserID, "владелец из идентичности")
	require.Len(t, svc.gotInput.Attributes, 1)
	assert.Equal(t, "brand", svc.gotInput.Attributes[0].AttributeSlug)

	var resp adResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ad-1", resp.ID)
	assert.Equal(t, "draft", resp.Status)
	require.Len(t, resp.Images, 1)
	assert.True(t, resp.Images[0].IsCover)
}

func TestCreate_CategoryNotFound(t *testing.T) {
	svc := &fakeService{createErr: app.ErrCategoryNotFound}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads", "user-1", validBody)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "category_not_found")
}

func TestCreate_UnknownAttribute(t *testing.T) {
	svc := &fakeService{createErr: domain.ErrUnknownAttribute}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads", "user-1", validBody)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "unknown_attribute")
}

func TestCreate_MissingRequiredAttribute(t *testing.T) {
	svc := &fakeService{createErr: domain.ErrMissingRequiredAttr}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads", "user-1", validBody)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing_required_attribute")
}

func TestCreate_InvalidJSON(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads", "user-1", `{"unknown_field":1}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_json")
}

func TestGet_OK(t *testing.T) {
	svc := &fakeService{getResult: domain.Ad{
		ID: "ad-1", UserID: "u", CategoryID: "cat", Title: "BMW", Price: 100, Currency: "UZS",
		RegionID: 1, Status: domain.StatusActive, CreatedAt: time.Unix(0, 0).UTC(),
		Attributes: []domain.AdAttributeValue{{AttributeSlug: "brand", Value: "bmw"}},
	}}
	rec := do(t, newRouter(svc), http.MethodGet, "/api/v1/ads/ad-1", "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var resp adResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ad-1", resp.ID)
	assert.Equal(t, "active", resp.Status)
	require.Len(t, resp.Attributes, 1)
	assert.Equal(t, "bmw", resp.Attributes[0].Value)
}

func TestGet_NotFound(t *testing.T) {
	svc := &fakeService{getErr: domain.ErrAdNotFound}
	rec := do(t, newRouter(svc), http.MethodGet, "/api/v1/ads/nope", "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "ad_not_found")
}
