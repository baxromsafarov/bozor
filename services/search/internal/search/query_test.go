package search

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearch_BuildsQueryAndDecodes(t *testing.T) {
	var got url.Values
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/collections/ads/documents/search", r.URL.Path)
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{
			"found": 2, "page": 1,
			"hits": [
				{"document": {"id":"ad-1","title":"BMW"}, "text_match": 42}
			],
			"facet_counts": [
				{"field_name":"currency","counts":[{"value":"USD","count":2}]}
			]
		}`))
	})

	res, err := c.Search(context.Background(), "ads", SearchParams{
		Q: "bmw", QueryBy: "title,description", QueryByWeights: "2,1",
		FilterBy: "currency:=`USD`", SortBy: "price:asc",
		FacetBy: "currency", MaxFacetValues: 50, Page: 1, PerPage: 20,
	})
	require.NoError(t, err)

	// Параметры переданы в Typesense.
	assert.Equal(t, "bmw", got.Get("q"))
	assert.Equal(t, "title,description", got.Get("query_by"))
	assert.Equal(t, "2,1", got.Get("query_by_weights"))
	assert.Equal(t, "currency:=`USD`", got.Get("filter_by"))
	assert.Equal(t, "price:asc", got.Get("sort_by"))
	assert.Equal(t, "currency", got.Get("facet_by"))
	assert.Equal(t, "50", got.Get("max_facet_values"))
	assert.Equal(t, "1", got.Get("page"))
	assert.Equal(t, "20", got.Get("per_page"))

	// Ответ декодирован.
	assert.Equal(t, 2, res.Found)
	require.Len(t, res.Hits, 1)
	assert.JSONEq(t, `{"id":"ad-1","title":"BMW"}`, string(res.Hits[0].Document))
	require.Len(t, res.FacetCounts, 1)
	assert.Equal(t, "currency", res.FacetCounts[0].FieldName)
	assert.Equal(t, "USD", res.FacetCounts[0].Counts[0].Value)
	assert.Equal(t, 2, res.FacetCounts[0].Counts[0].Count)
}

func TestSearch_OmitsEmptyOptionalParams(t *testing.T) {
	var got url.Values
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{"found":0,"hits":[]}`))
	})
	_, err := c.Search(context.Background(), "ads", SearchParams{Q: "*", QueryBy: "title"})
	require.NoError(t, err)
	assert.Equal(t, "*", got.Get("q"))
	_, hasFilter := got["filter_by"]
	assert.False(t, hasFilter, "пустой filter_by не передаётся")
	_, hasFacet := got["facet_by"]
	assert.False(t, hasFacet, "пустой facet_by не передаётся")
}

func TestSearch_Non200IsError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad filter"}`))
	})
	_, err := c.Search(context.Background(), "ads", SearchParams{Q: "*", QueryBy: "title"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}
