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
	gotInput   app.CreateInput
	createErr  error
	getResult  domain.Ad
	getErr     error
	lifeResult domain.Ad
	lifeErr    error
	gotAdID    string
	gotOwner   string
	gotUpdate  app.UpdateInput
	listResult []domain.Ad
	listErr    error
	gotFilter  domain.FeedFilter
	gotStatus  string
	gotLimit   int
	gotOffset  int
	gotAfter   string
	bumpResult bool
	bumpErr    error
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

func (f *fakeService) lifecycle(adID, userID string) (domain.Ad, error) {
	f.gotAdID, f.gotOwner = adID, userID
	if f.lifeErr != nil {
		return domain.Ad{}, f.lifeErr
	}
	return f.lifeResult, nil
}

func (f *fakeService) Update(_ context.Context, adID, userID string, in app.UpdateInput) (domain.Ad, error) {
	f.gotAdID, f.gotOwner, f.gotUpdate = adID, userID, in
	if f.lifeErr != nil {
		return domain.Ad{}, f.lifeErr
	}
	return f.lifeResult, nil
}

func (f *fakeService) Delete(_ context.Context, adID, userID string) error {
	f.gotAdID, f.gotOwner = adID, userID
	return f.lifeErr
}

func (f *fakeService) Feed(_ context.Context, filter domain.FeedFilter) ([]domain.Ad, error) {
	f.gotFilter = filter
	return f.listResult, f.listErr
}

func (f *fakeService) MyAds(_ context.Context, userID, status string, limit, offset int) ([]domain.Ad, error) {
	f.gotOwner, f.gotStatus = userID, status
	f.gotLimit, f.gotOffset = limit, offset
	return f.listResult, f.listErr
}

func (f *fakeService) ExportByID(_ context.Context, id string) (domain.Ad, error) {
	f.gotAdID = id
	if f.getErr != nil {
		return domain.Ad{}, f.getErr
	}
	return f.getResult, nil
}

func (f *fakeService) ExportActive(_ context.Context, after string, limit int) ([]domain.Ad, error) {
	f.gotAfter, f.gotLimit = after, limit
	return f.listResult, f.listErr
}

func (f *fakeService) SubmitForModeration(_ context.Context, adID, userID string) (domain.Ad, error) {
	return f.lifecycle(adID, userID)
}

func (f *fakeService) MarkSold(_ context.Context, adID, userID string) (domain.Ad, error) {
	return f.lifecycle(adID, userID)
}

func (f *fakeService) Renew(_ context.Context, adID, userID string) (domain.Ad, error) {
	return f.lifecycle(adID, userID)
}

func (f *fakeService) Archive(_ context.Context, adID, userID string) (domain.Ad, error) {
	return f.lifecycle(adID, userID)
}

func (f *fakeService) Bump(_ context.Context, adID string) (bool, error) {
	f.gotAdID = adID
	return f.bumpResult, f.bumpErr
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

func TestSubmit_AnonUnauthorized(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads/ad-1/submit", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, svc.gotAdID, "аноним не доходит до use-case")
}

func TestSubmit_HappyPath(t *testing.T) {
	svc := &fakeService{lifeResult: domain.Ad{
		ID: "ad-1", UserID: "user-1", Status: domain.StatusPending, CreatedAt: time.Unix(0, 0).UTC(),
	}}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads/ad-1/submit", "user-1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ad-1", svc.gotAdID)
	assert.Equal(t, "user-1", svc.gotOwner, "владелец из идентичности")

	var resp adResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "pending", resp.Status)
}

func TestSold_Forbidden(t *testing.T) {
	svc := &fakeService{lifeErr: app.ErrForbidden}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads/ad-1/sold", "intruder", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "forbidden")
}

func TestRenew_InvalidTransitionConflict(t *testing.T) {
	svc := &fakeService{lifeErr: domain.ErrInvalidTransition}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads/ad-1/renew", "user-1", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_transition")
}

func TestArchive_NotFound(t *testing.T) {
	svc := &fakeService{lifeErr: domain.ErrAdNotFound}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/ads/ad-1/archive", "user-1", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "ad_not_found")
}

func TestUpdate_HappyPath(t *testing.T) {
	svc := &fakeService{lifeResult: domain.Ad{ID: "ad-1", Status: domain.StatusPending, CreatedAt: time.Unix(0, 0).UTC()}}
	rec := do(t, newRouter(svc), http.MethodPatch, "/api/v1/ads/ad-1", "user-1", `{"price":999,"phone_display":false}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "user-1", svc.gotOwner)
	require.NotNil(t, svc.gotUpdate.Price)
	assert.Equal(t, int64(999), *svc.gotUpdate.Price)
	require.NotNil(t, svc.gotUpdate.PhoneDisplay)
	assert.False(t, *svc.gotUpdate.PhoneDisplay)
}

func TestUpdate_AnonUnauthorized(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newRouter(svc), http.MethodPatch, "/api/v1/ads/ad-1", "", `{"price":1}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUpdate_NotEditableConflict(t *testing.T) {
	svc := &fakeService{lifeErr: domain.ErrNotEditable}
	rec := do(t, newRouter(svc), http.MethodPatch, "/api/v1/ads/ad-1", "user-1", `{"title":"x"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "not_editable")
}

func TestDelete_NoContent(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newRouter(svc), http.MethodDelete, "/api/v1/ads/ad-1", "user-1", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "ad-1", svc.gotAdID)
}

func TestDelete_AnonUnauthorized(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newRouter(svc), http.MethodDelete, "/api/v1/ads/ad-1", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestFeed_PublicWithParams(t *testing.T) {
	svc := &fakeService{listResult: []domain.Ad{
		{ID: "a1", Status: domain.StatusActive, CreatedAt: time.Unix(0, 0).UTC()},
	}}
	rec := do(t, newRouter(svc), http.MethodGet, "/api/v1/ads?limit=5&category_id=cat&region_id=3&sort=price_asc", "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "cat", svc.gotFilter.CategoryID)
	assert.Equal(t, int16(3), svc.gotFilter.RegionID)
	assert.Equal(t, "price_asc", svc.gotFilter.Sort)
	assert.Equal(t, 5, svc.gotFilter.Limit)

	var resp listResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Ads, 1)
	assert.Equal(t, "a1", resp.Ads[0].ID)
}

func TestMyAds_RequiresAuth(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newRouter(svc), http.MethodGet, "/api/v1/me/ads", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMyAds_HappyPath(t *testing.T) {
	svc := &fakeService{listResult: []domain.Ad{{ID: "a1", CreatedAt: time.Unix(0, 0).UTC()}}}
	rec := do(t, newRouter(svc), http.MethodGet, "/api/v1/me/ads?status=active&limit=10&offset=2", "user-1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "user-1", svc.gotOwner)
	assert.Equal(t, "active", svc.gotStatus)
	assert.Equal(t, 10, svc.gotLimit)
	assert.Equal(t, 2, svc.gotOffset)
}

func TestMyAds_InvalidStatus(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newRouter(svc), http.MethodGet, "/api/v1/me/ads?status=weird", "user-1", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_status")
}

func TestExportGet_NoAuthNeeded(t *testing.T) {
	svc := &fakeService{getResult: domain.Ad{
		ID: "ad-1", Status: domain.StatusActive, Title: "T", CreatedAt: time.Unix(0, 0).UTC(),
		Attributes: []domain.AdAttributeValue{{AttributeSlug: "brand", Value: "bmw"}},
	}}
	rec := do(t, newRouter(svc), http.MethodGet, "/internal/ads/ad-1", "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ad-1", svc.gotAdID)
	var resp exportAd
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "active", resp.Status)
	require.Len(t, resp.Attributes, 1)
	assert.Equal(t, "bmw", resp.Attributes[0].Value)
}

func TestExportGet_NotFound(t *testing.T) {
	svc := &fakeService{getErr: domain.ErrAdNotFound}
	rec := do(t, newRouter(svc), http.MethodGet, "/internal/ads/nope", "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBump_OK(t *testing.T) {
	svc := &fakeService{bumpResult: true}
	rec := do(t, newRouter(svc), http.MethodPost, "/internal/ads/ad-1/bump", "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ad-1", svc.gotAdID)
	assert.Contains(t, rec.Body.String(), "bumped")
}

func TestBump_NotActive_404(t *testing.T) {
	svc := &fakeService{bumpResult: false}
	rec := do(t, newRouter(svc), http.MethodPost, "/internal/ads/ad-1/bump", "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "ad_not_bumpable")
}

func TestExportList_Keyset(t *testing.T) {
	svc := &fakeService{listResult: []domain.Ad{
		{ID: "a1", Status: domain.StatusActive, CreatedAt: time.Unix(0, 0).UTC()},
		{ID: "a2", Status: domain.StatusActive, CreatedAt: time.Unix(0, 0).UTC()},
	}}
	rec := do(t, newRouter(svc), http.MethodGet, "/internal/ads/export?after=a0&limit=2", "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "a0", svc.gotAfter)
	var resp exportListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Ads, 2)
	assert.Equal(t, "a2", resp.NextAfter, "курсор — id последнего")
}
