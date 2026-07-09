package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// SearchParams — низкоуровневые параметры запроса поиска к Typesense
// (собираются прикладным слоем из доменного запроса).
type SearchParams struct {
	Q              string // строка запроса ("*" — все документы)
	QueryBy        string // поля полнотекста, через запятую
	QueryByWeights string // веса полей query_by, через запятую
	FilterBy       string // выражение фильтра Typesense (filter_by)
	SortBy         string // сортировка (sort_by)
	FacetBy        string // поля фасетирования, через запятую
	MaxFacetValues int    // предел значений на фасет
	Page           int    // страница (1-based)
	PerPage        int    // размер страницы
}

// Hit — одно попадание поиска: сырой документ и релевантность.
type Hit struct {
	Document  json.RawMessage `json:"document"`
	TextMatch int64           `json:"text_match"`
}

// FacetValue — значение фасета и его количество.
type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// FacetCount — счётчики фасета по одному полю.
type FacetCount struct {
	FieldName string       `json:"field_name"`
	Counts    []FacetValue `json:"counts"`
}

// SearchResult — ответ Typesense на запрос поиска.
type SearchResult struct {
	Found       int          `json:"found"`
	Page        int          `json:"page"`
	Hits        []Hit        `json:"hits"`
	FacetCounts []FacetCount `json:"facet_counts"`
}

// Search выполняет полнотекстовый/фасетный поиск по коллекции
// (GET /collections/{name}/documents/search).
func (c *Client) Search(ctx context.Context, collection string, p SearchParams) (*SearchResult, error) {
	q := url.Values{}
	q.Set("q", p.Q)
	q.Set("query_by", p.QueryBy)
	if p.QueryByWeights != "" {
		q.Set("query_by_weights", p.QueryByWeights)
	}
	if p.FilterBy != "" {
		q.Set("filter_by", p.FilterBy)
	}
	if p.SortBy != "" {
		q.Set("sort_by", p.SortBy)
	}
	if p.FacetBy != "" {
		q.Set("facet_by", p.FacetBy)
		if p.MaxFacetValues > 0 {
			q.Set("max_facet_values", strconv.Itoa(p.MaxFacetValues))
		}
	}
	if p.Page > 0 {
		q.Set("page", strconv.Itoa(p.Page))
	}
	if p.PerPage > 0 {
		q.Set("per_page", strconv.Itoa(p.PerPage))
	}

	status, body, err := c.do(ctx, http.MethodGet, "/collections/"+collection+"/documents/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("typesense: поиск в %q status %d: %s", collection, status, body)
	}
	var res SearchResult
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("typesense: разбор результата поиска: %w", err)
	}
	return &res, nil
}
