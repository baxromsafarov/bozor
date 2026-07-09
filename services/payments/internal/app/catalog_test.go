package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func ptrInt(v int) *int       { return &v }
func ptrStr(v string) *string { return &v }

type fakeStore struct {
	services   []domain.Service
	bundles    []domain.Bundle
	rules      []domain.PriceRule
	gotRegion  *int
	gotCat     *string
	serviceErr error
	rulesErr   error
}

func (f *fakeStore) Services(context.Context) ([]domain.Service, error) {
	return f.services, f.serviceErr
}
func (f *fakeStore) Bundles(context.Context) ([]domain.Bundle, error) { return f.bundles, nil }
func (f *fakeStore) PriceRules(_ context.Context, region *int, cat *string) ([]domain.PriceRule, error) {
	f.gotRegion, f.gotCat = region, cat
	return f.rules, f.rulesErr
}

func sampleStore() *fakeStore {
	return &fakeStore{
		services: []domain.Service{
			{Code: "TOP", NameRU: "Топ", Durations: []int{7, 30}, SortOrder: 1},
			{Code: "BUMP", NameRU: "Поднятие", Durations: []int{0}, SortOrder: 2},
		},
		bundles: []domain.Bundle{
			{Code: "FAST_SALE", NameRU: "Быстрая продажа", Items: []domain.BundleItem{
				{ServiceCode: "TOP", Duration: 7},
				{ServiceCode: "BUMP", Duration: 0, BumpSchedule: []int{2, 4, 6}},
			}},
			{Code: "NO_PRICE", NameRU: "Без цены"},
		},
		rules: []domain.PriceRule{
			{ProductType: domain.ProductService, ProductCode: "TOP", Duration: 7, AmountUZS: 30000},
			{ProductType: domain.ProductService, ProductCode: "TOP", Duration: 30, AmountUZS: 90000},
			{ProductType: domain.ProductService, ProductCode: "BUMP", Duration: 0, AmountUZS: 5000},
			{ProductType: domain.ProductBundle, ProductCode: "FAST_SALE", Duration: 0, AmountUZS: 45000},
		},
	}
}

// TestGetCatalog_BuildsPricedServicesAndBundles — цены привязаны к длительностям,
// набор с ценой помечен Priced, без цены — Priced=false.
func TestGetCatalog_BuildsPricedServicesAndBundles(t *testing.T) {
	store := sampleStore()
	svc := NewService(store, discardLog())

	cat, err := svc.GetCatalog(context.Background(), nil, nil)
	require.NoError(t, err)

	assert.Equal(t, domain.CurrencyUZS, cat.Currency)
	require.Len(t, cat.Services, 2)

	top := cat.Services[0]
	assert.Equal(t, "TOP", top.Code)
	require.Len(t, top.Options, 2)
	assert.Equal(t, PriceOption{Duration: 7, AmountUZS: 30000}, top.Options[0])
	assert.Equal(t, PriceOption{Duration: 30, AmountUZS: 90000}, top.Options[1])

	require.Len(t, cat.Bundles, 2)
	fast := cat.Bundles[0]
	assert.Equal(t, "FAST_SALE", fast.Code)
	assert.True(t, fast.Priced)
	assert.EqualValues(t, 45000, fast.AmountUZS)
	assert.Len(t, fast.Items, 2)

	noPrice := cat.Bundles[1]
	assert.False(t, noPrice.Priced, "набор без строки цены не помечен Priced")
	assert.Zero(t, noPrice.AmountUZS)
}

// TestGetCatalog_OmitsDurationsWithoutPrice — длительность без цены выпадает из
// опций (например, если прайс на неё не задан под данные регион/категорию).
func TestGetCatalog_OmitsDurationsWithoutPrice(t *testing.T) {
	store := sampleStore()
	// Убираем цену на TOP/30.
	store.rules = store.rules[:1] // только TOP/7
	svc := NewService(store, discardLog())

	cat, err := svc.GetCatalog(context.Background(), nil, nil)
	require.NoError(t, err)

	top := cat.Services[0]
	require.Len(t, top.Options, 1)
	assert.Equal(t, 7, top.Options[0].Duration)
}

// TestGetCatalog_PassesRegionAndCategoryToStore — фильтр цен получает регион/категорию.
func TestGetCatalog_PassesRegionAndCategoryToStore(t *testing.T) {
	store := sampleStore()
	svc := NewService(store, discardLog())

	_, err := svc.GetCatalog(context.Background(), ptrInt(1), ptrStr("cat-1"))
	require.NoError(t, err)
	require.NotNil(t, store.gotRegion)
	assert.Equal(t, 1, *store.gotRegion)
	require.NotNil(t, store.gotCat)
	assert.Equal(t, "cat-1", *store.gotCat)
}

// TestGetCatalog_PropagatesStoreError — ошибка стора всплывает наверх.
func TestGetCatalog_PropagatesStoreError(t *testing.T) {
	store := sampleStore()
	store.rulesErr = errors.New("db down")
	svc := NewService(store, discardLog())

	_, err := svc.GetCatalog(context.Background(), nil, nil)
	assert.Error(t, err)
}
