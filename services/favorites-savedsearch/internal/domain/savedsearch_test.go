package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func i64(v int64) *int64 { return &v }

func sampleAd() AdView {
	city := int64(100)
	return AdView{
		ID: "ad-1", CategoryID: "cars", RegionID: 14, CityID: &city,
		Price: 4000, Currency: "USD", Title: "BMW X5 2015", Description: "отличное состояние",
		Attrs: map[string]string{"brand": "bmw", "year": "2015"},
	}
}

func TestSearchQuery_Matches(t *testing.T) {
	ad := sampleAd()
	tests := []struct {
		name  string
		query SearchQuery
		want  bool
	}{
		{"пустой запрос — совпадает всё", SearchQuery{}, true},
		{"категория совпадает", SearchQuery{CategoryID: "cars"}, true},
		{"категория не совпадает", SearchQuery{CategoryID: "phones"}, false},
		{"регион совпадает", SearchQuery{RegionID: 14}, true},
		{"регион не совпадает", SearchQuery{RegionID: 2}, false},
		{"город совпадает", SearchQuery{CityID: 100}, true},
		{"город не совпадает", SearchQuery{CityID: 200}, false},
		{"цена в диапазоне", SearchQuery{PriceMin: i64(1000), PriceMax: i64(5000)}, true},
		{"цена ниже минимума", SearchQuery{PriceMin: i64(5000)}, false},
		{"цена выше максимума", SearchQuery{PriceMax: i64(3000)}, false},
		{"валюта совпадает (без регистра)", SearchQuery{Currency: "usd"}, true},
		{"валюта не совпадает", SearchQuery{Currency: "UZS"}, false},
		{"атрибут совпадает", SearchQuery{Attrs: map[string]string{"brand": "bmw"}}, true},
		{"атрибут не совпадает", SearchQuery{Attrs: map[string]string{"brand": "audi"}}, false},
		{"несколько атрибутов — все совпадают", SearchQuery{Attrs: map[string]string{"brand": "bmw", "year": "2015"}}, true},
		{"несколько атрибутов — один нет", SearchQuery{Attrs: map[string]string{"brand": "bmw", "year": "2020"}}, false},
		{"текст-подстрока в заголовке", SearchQuery{Text: "bmw"}, true},
		{"текст-подстрока в описании", SearchQuery{Text: "состояние"}, true},
		{"текст не найден", SearchQuery{Text: "audi"}, false},
		{"комбинация всё совпадает", SearchQuery{CategoryID: "cars", RegionID: 14, PriceMax: i64(5000), Attrs: map[string]string{"brand": "bmw"}}, true},
		{"комбинация — цена ломает", SearchQuery{CategoryID: "cars", PriceMax: i64(1000)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.query.Matches(ad))
		})
	}
}

func TestSearchQuery_Matches_NilCity(t *testing.T) {
	ad := sampleAd()
	ad.CityID = nil
	assert.False(t, SearchQuery{CityID: 100}.Matches(ad), "город задан в поиске, но у объявления нет")
	assert.True(t, SearchQuery{}.Matches(ad), "город не задан — не важно")
}

func TestValidateName(t *testing.T) {
	require.NoError(t, ValidateName("Машины в Ташкенте"))
	require.ErrorIs(t, ValidateName(""), ErrEmptyName)
	require.ErrorIs(t, ValidateName("   "), ErrEmptyName)
	require.ErrorIs(t, ValidateName(strings.Repeat("я", MaxSavedSearchName+1)), ErrNameTooLong)
	require.NoError(t, ValidateName(strings.Repeat("я", MaxSavedSearchName)))
}
