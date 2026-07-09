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

	"bozor/services/payments/internal/app"
	"bozor/services/payments/internal/domain"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeCatalog struct {
	gotRegion *int
	gotCat    *string
}

func (f *fakeCatalog) GetCatalog(_ context.Context, region *int, cat *string) (app.Catalog, error) {
	f.gotRegion, f.gotCat = region, cat
	return app.Catalog{
		Currency:   domain.CurrencyUZS,
		RegionID:   region,
		CategoryID: cat,
		Services: []app.PricedService{{
			Service: domain.Service{Code: "TOP", NameRU: "Топ", Durations: []int{7}},
			Options: []app.PriceOption{{Duration: 7, AmountUZS: 30000}},
		}},
		Bundles: []app.PricedBundle{
			{Bundle: domain.Bundle{Code: "FAST_SALE", Items: []domain.BundleItem{{ServiceCode: "TOP", Duration: 7}}}, AmountUZS: 45000, Priced: true},
			{Bundle: domain.Bundle{Code: "NO_PRICE"}},
		},
	}, nil
}

func server(fc *fakeCatalog) http.Handler {
	return NewRouter(Deps{Log: discardLog(), Catalog: NewCatalogHandler(fc, discardLog())})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestCatalog_OK_BasePrices(t *testing.T) {
	fc := &fakeCatalog{}
	rec := get(t, server(fc), "/api/v1/promotions/catalog")
	require.Equal(t, http.StatusOK, rec.Code)

	var body catalogDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "UZS", body.Currency)
	assert.Nil(t, body.RegionID)
	require.Len(t, body.Services, 1)
	assert.Equal(t, "TOP", body.Services[0].Code)
	require.Len(t, body.Services[0].Options, 1)
	assert.EqualValues(t, 30000, body.Services[0].Options[0].AmountUZS)

	require.Len(t, body.Bundles, 2)
	require.NotNil(t, body.Bundles[0].AmountUZS)
	assert.EqualValues(t, 45000, *body.Bundles[0].AmountUZS)
	assert.Nil(t, body.Bundles[1].AmountUZS, "набор без цены отдаёт null")
	assert.Nil(t, fc.gotRegion, "без query — регион не задан")
	assert.Nil(t, fc.gotCat)
}

func TestCatalog_WithRegionAndCategory(t *testing.T) {
	fc := &fakeCatalog{}
	catID := "a0000000-0000-4000-8000-000000000002"
	rec := get(t, server(fc), "/api/v1/promotions/catalog?region_id=1&category_id="+catID)
	require.Equal(t, http.StatusOK, rec.Code)

	require.NotNil(t, fc.gotRegion)
	assert.Equal(t, 1, *fc.gotRegion)
	require.NotNil(t, fc.gotCat)
	assert.Equal(t, catID, *fc.gotCat)
}

func TestCatalog_InvalidRegion_422(t *testing.T) {
	for _, raw := range []string{"abc", "0", "-3"} {
		rec := get(t, server(&fakeCatalog{}), "/api/v1/promotions/catalog?region_id="+raw)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "region_id=%q", raw)
	}
}

func TestCatalog_InvalidCategory_422(t *testing.T) {
	rec := get(t, server(&fakeCatalog{}), "/api/v1/promotions/catalog?category_id=not-a-uuid")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}
