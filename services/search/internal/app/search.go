// Package app — use-cases Search-сервиса: перевод доменного запроса в параметры
// Typesense (read-модель ads) и проекция результата в доменные структуры. Слой
// поиска изолирован интерфейсом Docs — движок можно заменить, не трогая API
// (ADR-022, Stage 4.3).
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"bozor/services/search/internal/search"
)

// Пределы и значения по умолчанию.
const (
	defaultPerPage = 20
	maxPerPage     = 100
	maxFacetValues = 50
	// queryByFields — поля полнотекста; заголовок весомее описания.
	queryByFields  = "title,description"
	queryByWeights = "2,1"
	// TopBlockSize — максимум TOP-объявлений в топ-блоке выдачи (заглушка до
	// Promotions, CHAPTER 8; ротация «макс. 5 на категорию» — Stage 8.6).
	TopBlockSize = 5
	// topSort — порядок топ-блока: выше promotion_rank, затем свежесть.
	topSort = "promotion_rank:desc,created_at:desc"
)

// Режимы сортировки результата (значения параметра sort).
const (
	SortRelevance = "relevance"
	SortPriceAsc  = "price_asc"
	SortPriceDesc = "price_desc"
	SortDate      = "date"
	SortBumped    = "bumped"
	SortDistance  = "distance"
)

// Стандартные поля фасетирования (эндпоинт facets).
var defaultFacetFields = []string{"category_id", "region_id", "city_id", "currency"}

// Geo — точка и радиус (км) для гео-поиска/сортировки по удалённости.
type Geo struct {
	Lat      float64
	Lng      float64
	RadiusKm float64
}

// Query — доменные параметры поиска объявлений (независимы от Typesense).
type Query struct {
	Text       string
	CategoryID string
	RegionID   int
	CityID     int64
	PriceMin   *int64
	PriceMax   *int64
	Currency   string
	Attrs      map[string]string // attrs.<slug> == value (точный фильтр)
	Sort       string
	Geo        *Geo
	Page       int
	PerPage    int
	AttrFacets []string // доп. слаги атрибутов для фасетирования (эндпоинт facets)
}

// AdHit — объявление в результатах поиска (проекция документа Typesense).
type AdHit struct {
	ID          string
	Title       string
	Description string
	CategoryID  string
	RegionID    int
	CityID      *int64
	Price       int64
	Currency    string
	Attrs       map[string]string
	CreatedAt   int64
	BumpedAt    *int64
	IsTop       bool
	Location    []float64
}

// FacetValue — значение фасета и количество совпадений.
type FacetValue struct {
	Value string
	Count int
}

// Facet — распределение значений одного поля.
type Facet struct {
	Field  string
	Values []FacetValue
}

// Result — результат поиска: попадания, общее число, страница и топ-блок
// (featured/TOP-объявления над основной выдачей — заглушка до Promotions).
type Result struct {
	Hits    []AdHit
	Found   int
	Page    int
	PerPage int
	Top     []AdHit
}

// Docs — операции поиска над read-моделью (реализуется search.Client).
type Docs interface {
	Search(ctx context.Context, collection string, p search.SearchParams) (*search.SearchResult, error)
}

// Service реализует use-cases поиска.
type Service struct {
	docs Docs
}

// NewService создаёт сервис поиска.
func NewService(docs Docs) *Service { return &Service{docs: docs} }

// Search выполняет поиск объявлений и возвращает страницу попаданий. На первой
// странице дополнительно наполняется топ-блок (featured/TOP) — заглушка до
// Promotions (CHAPTER 8): пока is_top не проставляется, блок пуст.
func (s *Service) Search(ctx context.Context, q Query) (Result, error) {
	res, err := s.docs.Search(ctx, search.AdsCollection, buildParams(q, false))
	if err != nil {
		return Result{}, err
	}
	out := toResult(res, q)
	if clampPage(q.Page) == 1 {
		top, err := s.topBlock(ctx, q)
		if err != nil {
			return Result{}, err
		}
		out.Top = top
	}
	return out, nil
}

// topBlock возвращает TOP-объявления, релевантные фильтрам запроса (без учёта
// текста — это врезка промо по категории/региону, а не полнотекст).
func (s *Service) topBlock(ctx context.Context, q Query) ([]AdHit, error) {
	res, err := s.docs.Search(ctx, search.AdsCollection, buildTopParams(q))
	if err != nil {
		return nil, err
	}
	return decodeHits(res.Hits), nil
}

// Facets возвращает фасетное распределение по тем же фильтрам (без выборки
// документов — per_page минимален).
func (s *Service) Facets(ctx context.Context, q Query) ([]Facet, error) {
	res, err := s.docs.Search(ctx, search.AdsCollection, buildParams(q, true))
	if err != nil {
		return nil, err
	}
	return toFacets(res), nil
}

// buildParams переводит доменный запрос в параметры Typesense. При facetsOnly
// добавляет facet_by и минимизирует выборку документов.
func buildParams(q Query, facetsOnly bool) search.SearchParams {
	text := strings.TrimSpace(q.Text)
	if text == "" {
		text = "*"
	}
	p := search.SearchParams{
		Q:              text,
		QueryBy:        queryByFields,
		QueryByWeights: queryByWeights,
		FilterBy:       buildFilter(q),
		SortBy:         buildSort(q, text),
		Page:           clampPage(q.Page),
		PerPage:        clampPerPage(q.PerPage),
	}
	if facetsOnly {
		p.FacetBy = buildFacetBy(q)
		p.MaxFacetValues = maxFacetValues
		p.PerPage = 1 // документы не нужны — только facet_counts
	}
	return p
}

// buildTopParams собирает запрос топ-блока: фильтры запроса плюс is_top:=true,
// сортировка по промо-рангу, без учёта текста (q='*').
func buildTopParams(q Query) search.SearchParams {
	filter := buildFilter(q)
	if filter != "" {
		filter += " && is_top:=true"
	} else {
		filter = "is_top:=true"
	}
	return search.SearchParams{
		Q:              "*",
		QueryBy:        queryByFields,
		QueryByWeights: queryByWeights,
		FilterBy:       filter,
		SortBy:         topSort,
		Page:           1,
		PerPage:        TopBlockSize,
	}
}

// buildFilter собирает выражение filter_by из фильтров запроса. Строковые
// значения оборачиваются в бэктики (точное совпадение, безопасно к пробелам),
// имена динамических полей санитизируются — защита от инъекции в фильтр.
func buildFilter(q Query) string {
	var parts []string
	if q.CategoryID != "" {
		parts = append(parts, "category_id:="+quote(q.CategoryID))
	}
	if q.RegionID > 0 {
		parts = append(parts, "region_id:="+strconv.Itoa(q.RegionID))
	}
	if q.CityID > 0 {
		parts = append(parts, "city_id:="+strconv.FormatInt(q.CityID, 10))
	}
	if q.Currency != "" {
		parts = append(parts, "currency:="+quote(q.Currency))
	}
	if q.PriceMin != nil {
		parts = append(parts, "price:>="+strconv.FormatInt(*q.PriceMin, 10))
	}
	if q.PriceMax != nil {
		parts = append(parts, "price:<="+strconv.FormatInt(*q.PriceMax, 10))
	}
	for _, slug := range sortedKeys(q.Attrs) {
		field := sanitizeSlug(slug)
		if field == "" {
			continue
		}
		parts = append(parts, "attrs."+field+":="+quote(q.Attrs[slug]))
	}
	if q.Geo != nil && q.Geo.RadiusKm > 0 {
		parts = append(parts, fmt.Sprintf("location:(%s, %s, %s km)",
			ftoa(q.Geo.Lat), ftoa(q.Geo.Lng), ftoa(q.Geo.RadiusKm)))
	}
	return strings.Join(parts, " && ")
}

// buildSort выбирает sort_by по режиму. Релевантность без текста бессмысленна —
// вырождается в сортировку по дате.
func buildSort(q Query, text string) string {
	switch q.Sort {
	case SortPriceAsc:
		return "price:asc"
	case SortPriceDesc:
		return "price:desc"
	case SortDate:
		return "created_at:desc"
	case SortBumped:
		return "bumped_at:desc"
	case SortDistance:
		if q.Geo != nil {
			return fmt.Sprintf("location(%s, %s):asc", ftoa(q.Geo.Lat), ftoa(q.Geo.Lng))
		}
		return defaultSort(text)
	default: // relevance / пусто / неизвестное
		return defaultSort(text)
	}
}

// defaultSort — релевантность при наличии текста, иначе свежесть.
func defaultSort(text string) string {
	if text == "*" {
		return "created_at:desc"
	}
	return "_text_match:desc,created_at:desc"
}

// buildFacetBy собирает поля фасетирования: стандартные плюс запрошенные
// атрибуты (attrs.<slug>).
func buildFacetBy(q Query) string {
	fields := make([]string, 0, len(defaultFacetFields)+len(q.AttrFacets))
	fields = append(fields, defaultFacetFields...)
	for _, slug := range q.AttrFacets {
		if s := sanitizeSlug(slug); s != "" {
			fields = append(fields, "attrs."+s)
		}
	}
	return strings.Join(fields, ",")
}

// toResult проецирует ответ Typesense в результат поиска.
func toResult(res *search.SearchResult, q Query) Result {
	return Result{
		Found:   res.Found,
		Page:    clampPage(q.Page),
		PerPage: clampPerPage(q.PerPage),
		Hits:    decodeHits(res.Hits),
	}
}

// decodeHits проецирует документы Typesense в попадания (нечитаемые пропускаются).
func decodeHits(hits []search.Hit) []AdHit {
	out := make([]AdHit, 0, len(hits))
	for _, h := range hits {
		var d adDocument
		if err := json.Unmarshal(h.Document, &d); err != nil {
			continue
		}
		out = append(out, d.toHit())
	}
	return out
}

// toFacets проецирует facet_counts Typesense в доменные фасеты.
func toFacets(res *search.SearchResult) []Facet {
	facets := make([]Facet, 0, len(res.FacetCounts))
	for _, fc := range res.FacetCounts {
		vals := make([]FacetValue, 0, len(fc.Counts))
		for _, c := range fc.Counts {
			vals = append(vals, FacetValue{Value: c.Value, Count: c.Count})
		}
		facets = append(facets, Facet{Field: fc.FieldName, Values: vals})
	}
	return facets
}

// adDocument — документ коллекции ads для декодирования (совпадает с проекцией
// индексатора).
type adDocument struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	CategoryID  string            `json:"category_id"`
	RegionID    int               `json:"region_id"`
	CityID      *int64            `json:"city_id"`
	Price       int64             `json:"price"`
	Currency    string            `json:"currency"`
	Attrs       map[string]string `json:"attrs"`
	CreatedAt   int64             `json:"created_at"`
	BumpedAt    *int64            `json:"bumped_at"`
	IsTop       bool              `json:"is_top"`
	Location    []float64         `json:"location"`
}

// toHit конвертирует документ в попадание (идентичный набор полей — различаются
// только json-теги, которые при конверсии игнорируются).
func (d adDocument) toHit() AdHit {
	return AdHit(d)
}

// quote оборачивает строковое значение фильтра в бэктики Typesense, вырезая
// сами бэктики из значения (нейтрализация выхода из литерала).
func quote(v string) string {
	return "`" + strings.ReplaceAll(v, "`", "") + "`"
}

// sanitizeSlug оставляет только [a-z0-9_-] — имя поля attrs.<slug> не должно
// вносить операторы в выражение фильтра.
func sanitizeSlug(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return -1
		}
	}, strings.ToLower(s))
}

// ftoa форматирует число с плавающей точкой без экспоненты (для гео-выражений).
func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// sortedKeys возвращает ключи map в детерминированном порядке (стабильный фильтр).
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// clampPage приводит номер страницы к минимуму 1.
func clampPage(p int) int {
	if p < 1 {
		return 1
	}
	return p
}

// clampPerPage приводит размер страницы к [1, maxPerPage] с дефолтом.
func clampPerPage(n int) int {
	switch {
	case n <= 0:
		return defaultPerPage
	case n > maxPerPage:
		return maxPerPage
	default:
		return n
	}
}
