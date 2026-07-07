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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/catalog/internal/app"
	"bozor/services/catalog/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeService подменяет use-cases каталога.
type fakeService struct {
	treeJSON  []byte
	created   domain.Category
	createErr error
	updateErr error
	deleteErr error
	gotCreate app.CreateInput
}

func (f *fakeService) TreeJSON(context.Context) ([]byte, error) {
	if f.treeJSON == nil {
		return []byte(`{"categories":[]}`), nil
	}
	return f.treeJSON, nil
}

func (f *fakeService) Create(_ context.Context, in app.CreateInput) (domain.Category, error) {
	f.gotCreate = in
	if f.createErr != nil {
		return domain.Category{}, f.createErr
	}
	return f.created, nil
}

func (f *fakeService) Update(_ context.Context, id string, _ app.UpdateInput) (domain.Category, error) {
	if f.updateErr != nil {
		return domain.Category{}, f.updateErr
	}
	return domain.Category{ID: id, Slug: "s", NameUZ: "u", NameRU: "r"}, nil
}

func (f *fakeService) Delete(context.Context, string) error { return f.deleteErr }

func newRouter(svc Service) http.Handler {
	return NewRouter(Deps{Log: discardLogger(), Handler: NewHandler(svc, discardLogger())})
}

// do выполняет запрос с опциональной ролью в forwarded-заголовках.
func do(t *testing.T, h http.Handler, method, path, roles, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if roles != "" {
		req.Header.Set("X-User-Id", "staff-1")
		req.Header.Set("X-User-Roles", roles)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTree_Public(t *testing.T) {
	svc := &fakeService{treeJSON: []byte(`{"categories":[{"slug":"electronics"}]}`)}
	rec := do(t, newRouter(svc), http.MethodGet, "/api/v1/categories", "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, rec.Body.String(), "electronics")
}

func TestCreate_AnonUnauthorized(t *testing.T) {
	rec := do(t, newRouter(&fakeService{}), http.MethodPost, "/api/v1/categories", "",
		`{"slug":"x","name_uz":"X","name_ru":"X"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "unauthorized")
}

func TestCreate_NonStaffForbidden(t *testing.T) {
	rec := do(t, newRouter(&fakeService{}), http.MethodPost, "/api/v1/categories", "user",
		`{"slug":"x","name_uz":"X","name_ru":"X"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "forbidden")
}

func TestCreate_AdminOK(t *testing.T) {
	svc := &fakeService{created: domain.Category{ID: "1", Slug: "electronics", NameUZ: "E", NameRU: "Э", Path: "electronics"}}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/categories", "user,admin",
		`{"slug":"electronics","name_uz":"E","name_ru":"Э"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp categoryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "electronics", resp.Slug)
	assert.True(t, svc.gotCreate.IsActive, "is_active по умолчанию true")
}

func TestCreate_ModeratorOK(t *testing.T) {
	svc := &fakeService{created: domain.Category{ID: "1", Slug: "s", NameUZ: "u", NameRU: "r"}}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/categories", "moderator",
		`{"slug":"s","name_uz":"u","name_ru":"r"}`)
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestCreate_ValidationError(t *testing.T) {
	rec := do(t, newRouter(&fakeService{}), http.MethodPost, "/api/v1/categories", "admin",
		`{"name_uz":"X"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_category")
}

func TestCreate_SlugConflict(t *testing.T) {
	svc := &fakeService{createErr: domain.ErrSlugConflict}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/categories", "admin",
		`{"slug":"dup","name_uz":"X","name_ru":"X"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "slug_conflict")
}

func TestCreate_ParentNotFound(t *testing.T) {
	svc := &fakeService{createErr: domain.ErrParentNotFound}
	rec := do(t, newRouter(svc), http.MethodPost, "/api/v1/categories", "admin",
		`{"slug":"x","name_uz":"X","name_ru":"X","parent_id":"missing"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "parent_not_found")
}

func TestDelete_HasChildrenConflict(t *testing.T) {
	svc := &fakeService{deleteErr: domain.ErrHasChildren}
	rec := do(t, newRouter(svc), http.MethodDelete, "/api/v1/categories/abc", "admin", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "has_children")
}

func TestDelete_NotFound(t *testing.T) {
	svc := &fakeService{deleteErr: domain.ErrCategoryNotFound}
	rec := do(t, newRouter(svc), http.MethodDelete, "/api/v1/categories/abc", "admin", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "category_not_found")
}

func TestUpdate_AdminOK(t *testing.T) {
	rec := do(t, newRouter(&fakeService{}), http.MethodPatch, "/api/v1/categories/abc", "admin",
		`{"name_uz":"Yangi","name_ru":"Новое","sort_order":2}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}
