package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func ptrInt(v int) *int       { return &v }
func ptrStr(v string) *string { return &v }

// TestSpecificity — регион конкретнее категории, сочетание — самое конкретное.
func TestSpecificity(t *testing.T) {
	base := PriceRule{}
	cat := PriceRule{CategoryID: ptrStr("c")}
	reg := PriceRule{RegionID: ptrInt(1)}
	both := PriceRule{RegionID: ptrInt(1), CategoryID: ptrStr("c")}

	assert.Equal(t, 0, base.Specificity())
	assert.Equal(t, 1, cat.Specificity())
	assert.Equal(t, 2, reg.Specificity())
	assert.Equal(t, 3, both.Specificity())
	assert.Greater(t, reg.Specificity(), cat.Specificity(), "регион важнее категории")
}

// TestResolvePrices_PicksMostSpecific — среди подходящих правил выбирается самое
// конкретное на каждый продукт×длительность.
func TestResolvePrices_PicksMostSpecific(t *testing.T) {
	rules := []PriceRule{
		{ProductType: ProductService, ProductCode: "TOP", Duration: 7, AmountUZS: 30000},                                  // база
		{ProductType: ProductService, ProductCode: "TOP", Duration: 7, AmountUZS: 40000, CategoryID: ptrStr("transport")}, // по категории
		{ProductType: ProductService, ProductCode: "TOP", Duration: 7, AmountUZS: 45000, RegionID: ptrInt(1)},             // по региону
		{ProductType: ProductService, ProductCode: "TOP", Duration: 7, AmountUZS: 60000, RegionID: ptrInt(1), CategoryID: ptrStr("transport")},
		{ProductType: ProductService, ProductCode: "TOP", Duration: 30, AmountUZS: 90000},
		{ProductType: ProductBundle, ProductCode: "FAST_SALE", Duration: 0, AmountUZS: 45000},
	}
	prices := ResolvePrices(rules)

	got, ok := ServicePrice(prices, "TOP", 7)
	assert.True(t, ok)
	assert.EqualValues(t, 60000, got, "регион+категория — самое конкретное")

	got, ok = ServicePrice(prices, "TOP", 30)
	assert.True(t, ok)
	assert.EqualValues(t, 90000, got)

	got, ok = BundlePrice(prices, "FAST_SALE")
	assert.True(t, ok)
	assert.EqualValues(t, 45000, got)

	_, ok = ServicePrice(prices, "TOP", 3)
	assert.False(t, ok, "нет цены на такую длительность")
}

// TestResolvePrices_OrderIndependent — результат не зависит от порядка правил.
func TestResolvePrices_OrderIndependent(t *testing.T) {
	specific := PriceRule{ProductType: ProductService, ProductCode: "TOP", Duration: 7, AmountUZS: 45000, RegionID: ptrInt(1)}
	base := PriceRule{ProductType: ProductService, ProductCode: "TOP", Duration: 7, AmountUZS: 30000}

	a := ResolvePrices([]PriceRule{base, specific})
	b := ResolvePrices([]PriceRule{specific, base})

	pa, _ := ServicePrice(a, "TOP", 7)
	pb, _ := ServicePrice(b, "TOP", 7)
	assert.EqualValues(t, 45000, pa)
	assert.Equal(t, pa, pb, "порядок правил не влияет на результат")
}
