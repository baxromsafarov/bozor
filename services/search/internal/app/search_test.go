package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/search/internal/search"
)

// fakeDocs фиксирует все запросы и отвечает по фильтру: топ-блок (is_top) —
// отдельным результатом, остальное — основным.
type fakeDocs struct {
	calls  []search.SearchParams
	res    *search.SearchResult // основной ответ
	topRes *search.SearchResult // ответ на запрос топ-блока (is_top:=true)
	err    error
}

func (f *fakeDocs) Search(_ context.Context, _ string, p search.SearchParams) (*search.SearchResult, error) {
	f.calls = append(f.calls, p)
	if f.err != nil {
		return &search.SearchResult{}, f.err
	}
	if strings.Contains(p.FilterBy, "is_top:=true") {
		if f.topRes != nil {
			return f.topRes, nil
		}
		return &search.SearchResult{}, nil
	}
	if f.res == nil {
		return &search.SearchResult{}, nil
	}
	return f.res, nil
}

// last возвращает параметры последнего запроса.
func (f *fakeDocs) last() search.SearchParams { return f.calls[len(f.calls)-1] }

func i64(v int64) *int64 { return &v }

func TestBuildParams_FiltersSortPagination(t *testing.T) {
	q := Query{
		Text:       "  bmw x5  ",
		CategoryID: "cars",
		RegionID:   14,
		CityID:     101,
		Currency:   "USD",
		PriceMin:   i64(1000),
		PriceMax:   i64(5000),
		Attrs:      map[string]string{"brand": "bmw", "color": "black"},
		Sort:       SortPriceAsc,
		Page:       2,
		PerPage:    30,
	}
	p := buildParams(q, false)

	assert.Equal(t, "bmw x5", p.Q, "текст обрезается")
	assert.Equal(t, "title,description", p.QueryBy)
	assert.Equal(t, "price:asc", p.SortBy)
	assert.Equal(t, 2, p.Page)
	assert.Equal(t, 30, p.PerPage)
	assert.Empty(t, p.FacetBy, "поиск без фасетов")

	// Фильтр детерминирован (attrs отсортированы), строки в бэктиках.
	assert.Equal(t,
		"category_id:=`cars` && region_id:=14 && city_id:=101 && currency:=`USD` && "+
			"price:>=1000 && price:<=5000 && attrs.brand:=`bmw` && attrs.color:=`black`",
		p.FilterBy)
}

func TestBuildParams_EmptyTextRelevanceFallsBackToDate(t *testing.T) {
	assert.Equal(t, "created_at:desc", buildParams(Query{}, false).SortBy, "без текста релевантность → дата")
	assert.Equal(t, "*", buildParams(Query{}, false).Q)

	withText := buildParams(Query{Text: "phone"}, false)
	assert.Equal(t, "_text_match:desc,created_at:desc", withText.SortBy, "с текстом — релевантность")
}

func TestBuildSort_Modes(t *testing.T) {
	geo := &Geo{Lat: 41.3, Lng: 69.2, RadiusKm: 5}
	assert.Equal(t, "price:desc", buildSort(Query{Sort: SortPriceDesc}, "x"))
	assert.Equal(t, "created_at:desc", buildSort(Query{Sort: SortDate}, "x"))
	assert.Equal(t, "bumped_at:desc", buildSort(Query{Sort: SortBumped}, "x"))
	assert.Equal(t, "location(41.3, 69.2):asc", buildSort(Query{Sort: SortDistance, Geo: geo}, "x"))
	// distance без гео вырождается в релевантность.
	assert.Equal(t, "_text_match:desc,created_at:desc", buildSort(Query{Sort: SortDistance}, "x"))
}

func TestBuildFilter_GeoRadius(t *testing.T) {
	q := Query{Geo: &Geo{Lat: 41.311, Lng: 69.279, RadiusKm: 10}}
	assert.Equal(t, "location:(41.311, 69.279, 10 km)", buildFilter(q))
}

func TestBuildFilter_SanitizesAttrSlug(t *testing.T) {
	// Слаг с попыткой инъекции оператора: остаются только [a-z0-9_-].
	q := Query{Attrs: map[string]string{"br and:=x || y": "bmw"}}
	assert.Equal(t, "attrs.brandxy:=`bmw`", buildFilter(q), "имя поля санитизируется")
}

func TestBuildFilter_QuoteStripsBacktick(t *testing.T) {
	q := Query{Currency: "US`D"}
	assert.Equal(t, "currency:=`USD`", buildFilter(q), "бэктик вырезается из значения")
}

func TestClampPerPage(t *testing.T) {
	assert.Equal(t, defaultPerPage, clampPerPage(0))
	assert.Equal(t, maxPerPage, clampPerPage(1000))
	assert.Equal(t, 50, clampPerPage(50))
	assert.Equal(t, 1, clampPage(0))
	assert.Equal(t, 3, clampPage(3))
}

func TestBuildParams_FacetsOnly(t *testing.T) {
	q := Query{CategoryID: "cars", AttrFacets: []string{"brand", "bad slug!"}}
	p := buildParams(q, true)
	assert.Equal(t, "category_id,region_id,city_id,currency,attrs.brand,attrs.badslug", p.FacetBy)
	assert.Equal(t, maxFacetValues, p.MaxFacetValues)
	assert.Equal(t, 1, p.PerPage, "фасетам документы не нужны")
	assert.Contains(t, p.FilterBy, "category_id:=`cars`", "фильтры применяются и к фасетам")
}

func TestService_Search_MapsHits(t *testing.T) {
	doc := adDocument{
		ID: "ad-1", Title: "BMW X5", CategoryID: "cars", RegionID: 14,
		CityID: i64(101), Price: 4000, Currency: "USD",
		Attrs: map[string]string{"brand": "bmw"}, CreatedAt: 1720000000,
		Location: []float64{41.3, 69.2},
	}
	raw, _ := json.Marshal(doc)
	docs := &fakeDocs{res: &search.SearchResult{
		Found: 1, Page: 1,
		Hits: []search.Hit{{Document: raw, TextMatch: 99}},
	}}

	res, err := NewService(docs).Search(context.Background(), Query{Text: "bmw"})
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	assert.Equal(t, 1, res.Found)
	h := res.Hits[0]
	assert.Equal(t, "ad-1", h.ID)
	assert.Equal(t, "bmw", h.Attrs["brand"])
	assert.Equal(t, []float64{41.3, 69.2}, h.Location)
	assert.Equal(t, defaultPerPage, res.PerPage)
}

func TestService_Search_SkipsUnreadableDocs(t *testing.T) {
	docs := &fakeDocs{res: &search.SearchResult{
		Found: 1,
		Hits:  []search.Hit{{Document: json.RawMessage(`{"id":123}`)}}, // id не строка
	}}
	res, err := NewService(docs).Search(context.Background(), Query{})
	require.NoError(t, err)
	assert.Empty(t, res.Hits, "нечитаемый документ пропущен")
}

func TestService_Facets_Maps(t *testing.T) {
	docs := &fakeDocs{res: &search.SearchResult{
		FacetCounts: []search.FacetCount{
			{FieldName: "category_id", Counts: []search.FacetValue{{Value: "cars", Count: 3}}},
		},
	}}
	facets, err := NewService(docs).Facets(context.Background(), Query{})
	require.NoError(t, err)
	require.Len(t, facets, 1)
	assert.Equal(t, "category_id", facets[0].Field)
	assert.Equal(t, "cars", facets[0].Values[0].Value)
	assert.Equal(t, 3, facets[0].Values[0].Count)
}

func docHit(id string) search.Hit {
	raw, _ := json.Marshal(adDocument{ID: id, Title: id, IsTop: true})
	return search.Hit{Document: raw}
}

func TestBuildTopParams(t *testing.T) {
	p := buildTopParams(Query{CategoryID: "cars", RegionID: 14}, 1720000000)
	assert.Equal(t, "*", p.Q, "топ-блок не зависит от текста")
	assert.Equal(t,
		"category_id:=`cars` && region_id:=14 && is_top:=true && promo_ends_at:>1720000000",
		p.FilterBy, "фильтры запроса + активные TOP")
	assert.Equal(t, "promotion_rank:desc,created_at:desc", p.SortBy)
	assert.Equal(t, topCandidatePool, p.PerPage, "набирается пул кандидатов для ротации")

	// Без фильтров — только активные TOP.
	assert.Equal(t, "is_top:=true && promo_ends_at:>1720000000", buildTopParams(Query{}, 1720000000).FilterBy)
}

func TestRotateTop(t *testing.T) {
	// Пул больше блока → возвращается ровно TopBlockSize, все из пула.
	pool := []AdHit{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}, {ID: "f"}, {ID: "g"}}
	got := rotateTop(pool)
	require.Len(t, got, TopBlockSize)
	valid := map[string]bool{"a": true, "b": true, "c": true, "d": true, "e": true, "f": true, "g": true}
	seen := map[string]bool{}
	for _, h := range got {
		assert.True(t, valid[h.ID], "объявление из пула кандидатов")
		assert.False(t, seen[h.ID], "без повторов")
		seen[h.ID] = true
	}

	// Пул не больше блока → возвращается как есть (по количеству).
	small := []AdHit{{ID: "x"}, {ID: "y"}}
	assert.Len(t, rotateTop(small), 2)
	assert.Empty(t, rotateTop(nil))
}

func TestService_Search_TopBlockOnFirstPage(t *testing.T) {
	docs := &fakeDocs{
		res:    &search.SearchResult{Found: 2, Hits: []search.Hit{docHit("ad-1")}},
		topRes: &search.SearchResult{Found: 1, Hits: []search.Hit{docHit("top-1")}},
	}
	res, err := NewService(docs).Search(context.Background(), Query{Text: "bmw", Page: 1})
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	require.Len(t, res.Top, 1, "топ-блок наполнен на первой странице")
	assert.Equal(t, "top-1", res.Top[0].ID)
	require.Len(t, docs.calls, 2, "основной запрос + запрос топ-блока")
	assert.Contains(t, docs.last().FilterBy, "is_top:=true")
}

func TestService_Search_NoTopBlockAfterFirstPage(t *testing.T) {
	docs := &fakeDocs{
		res:    &search.SearchResult{Found: 30, Hits: []search.Hit{docHit("ad-1")}},
		topRes: &search.SearchResult{Hits: []search.Hit{docHit("top-1")}},
	}
	res, err := NewService(docs).Search(context.Background(), Query{Page: 2})
	require.NoError(t, err)
	assert.Empty(t, res.Top, "топ-блок только на первой странице")
	require.Len(t, docs.calls, 1, "без запроса топ-блока")
}

func TestService_Search_TopBlockEmptyWhenNoTopAds(t *testing.T) {
	// topRes не задан → нет TOP-объявлений (состояние до Promotions).
	docs := &fakeDocs{res: &search.SearchResult{Found: 1, Hits: []search.Hit{docHit("ad-1")}}}
	res, err := NewService(docs).Search(context.Background(), Query{Page: 1})
	require.NoError(t, err)
	assert.Empty(t, res.Top)
}
