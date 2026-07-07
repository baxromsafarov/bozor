package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/catalog/internal/app"
	"bozor/services/catalog/internal/domain"
)

// fakeAttrService подменяет use-cases атрибутов.
type fakeAttrService struct {
	list        []domain.Attribute
	created     domain.Attribute
	createErr   error
	effective   []domain.EffectiveAttribute
	effErr      error
	linkErr     error
	gotCreate   app.CreateAttributeInput
	gotLinkAttr string
}

func (f *fakeAttrService) List(context.Context) ([]domain.Attribute, error) { return f.list, nil }

func (f *fakeAttrService) Get(_ context.Context, id string) (domain.Attribute, error) {
	return domain.Attribute{ID: id, Slug: "s", NameUZ: "u", NameRU: "r", Type: domain.TypeString}, nil
}

func (f *fakeAttrService) Create(_ context.Context, in app.CreateAttributeInput) (domain.Attribute, error) {
	f.gotCreate = in
	if f.createErr != nil {
		return domain.Attribute{}, f.createErr
	}
	return f.created, nil
}

func (f *fakeAttrService) Update(_ context.Context, id string, _ app.UpdateAttributeInput) (domain.Attribute, error) {
	return domain.Attribute{ID: id, Slug: "s", NameUZ: "u", NameRU: "r", Type: domain.TypeString}, nil
}

func (f *fakeAttrService) Delete(context.Context, string) error { return nil }

func (f *fakeAttrService) Effective(context.Context, string) ([]domain.EffectiveAttribute, error) {
	return f.effective, f.effErr
}

func (f *fakeAttrService) Link(_ context.Context, _, attributeID string, _ int) error {
	f.gotLinkAttr = attributeID
	return f.linkErr
}

func (f *fakeAttrService) Unlink(context.Context, string, string) error { return nil }

func newAttrRouter(svc AttributeService) http.Handler {
	return NewRouter(Deps{
		Log:        discardLogger(),
		Handler:    NewHandler(&fakeService{}, discardLogger()),
		AttributeH: NewAttributeHandler(svc, discardLogger()),
	})
}

func TestCategoryAttributes_Public(t *testing.T) {
	unit := "km"
	svc := &fakeAttrService{effective: []domain.EffectiveAttribute{
		{Attribute: domain.Attribute{ID: "a1", Slug: "brand", NameUZ: "Marka", NameRU: "Марка", Type: domain.TypeEnum}, Inherited: true, SortOrder: 1, SourceID: "root"},
		{Attribute: domain.Attribute{ID: "a2", Slug: "mileage", NameUZ: "Probeg", NameRU: "Пробег", Type: domain.TypeInt, Unit: &unit}, SourceID: "self"},
	}}
	rec := do(t, newAttrRouter(svc), http.MethodGet, "/api/v1/categories/cat1/attributes", "", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Attributes []map[string]any `json:"attributes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Attributes, 2)
	assert.Equal(t, true, resp.Attributes[0]["inherited"])
	assert.Equal(t, "brand", resp.Attributes[0]["slug"])
}

func TestCategoryAttributes_CategoryNotFound(t *testing.T) {
	svc := &fakeAttrService{effErr: domain.ErrCategoryNotFound}
	rec := do(t, newAttrRouter(svc), http.MethodGet, "/api/v1/categories/missing/attributes", "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "category_not_found")
}

func TestCreateAttribute_AnonUnauthorized(t *testing.T) {
	rec := do(t, newAttrRouter(&fakeAttrService{}), http.MethodPost, "/api/v1/attributes", "",
		`{"slug":"x","name_uz":"X","name_ru":"X","type":"string"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateAttribute_NonStaffForbidden(t *testing.T) {
	rec := do(t, newAttrRouter(&fakeAttrService{}), http.MethodPost, "/api/v1/attributes", "user",
		`{"slug":"x","name_uz":"X","name_ru":"X","type":"string"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateAttribute_ValidationMissingType(t *testing.T) {
	rec := do(t, newAttrRouter(&fakeAttrService{}), http.MethodPost, "/api/v1/attributes", "admin",
		`{"slug":"x","name_uz":"X","name_ru":"X"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_attribute")
}

func TestCreateAttribute_AdminOK(t *testing.T) {
	svc := &fakeAttrService{created: domain.Attribute{ID: "1", Slug: "deal", NameUZ: "B", NameRU: "С", Type: domain.TypeEnum}}
	rec := do(t, newAttrRouter(svc), http.MethodPost, "/api/v1/attributes", "admin",
		`{"slug":"deal","name_uz":"B","name_ru":"С","type":"enum","options":[{"slug":"sale","name_uz":"S","name_ru":"П"}]}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp attributeResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "deal", resp.Slug)
	require.Len(t, svc.gotCreate.Options, 1, "варианты переданы в use-case")
}

func TestCreateAttribute_EnumRequiresOptions(t *testing.T) {
	svc := &fakeAttrService{createErr: domain.ErrEnumRequiresOptions}
	rec := do(t, newAttrRouter(svc), http.MethodPost, "/api/v1/attributes", "admin",
		`{"slug":"deal","name_uz":"B","name_ru":"С","type":"enum"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "enum_requires_options")
}

func TestLinkAttribute_ValidationMissingID(t *testing.T) {
	rec := do(t, newAttrRouter(&fakeAttrService{}), http.MethodPost, "/api/v1/categories/cat1/attributes", "admin",
		`{"sort_order":1}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_link")
}

func TestLinkAttribute_AdminOK(t *testing.T) {
	svc := &fakeAttrService{}
	rec := do(t, newAttrRouter(svc), http.MethodPost, "/api/v1/categories/cat1/attributes", "admin",
		`{"attribute_id":"attr1","sort_order":2}`)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "attr1", svc.gotLinkAttr)
}

func TestLinkAttribute_Conflict(t *testing.T) {
	svc := &fakeAttrService{linkErr: domain.ErrLinkExists}
	rec := do(t, newAttrRouter(svc), http.MethodPost, "/api/v1/categories/cat1/attributes", "admin",
		`{"attribute_id":"attr1"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "link_exists")
}

func TestUnlinkAttribute_AdminOK(t *testing.T) {
	rec := do(t, newAttrRouter(&fakeAttrService{}), http.MethodDelete,
		"/api/v1/categories/cat1/attributes/attr1", "admin", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
