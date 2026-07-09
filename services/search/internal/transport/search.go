package transport

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/search/internal/app"
)

// attrParamPrefix — префикс query-параметра фильтра по атрибуту: attr.<slug>=value.
const attrParamPrefix = "attr."

// Searcher — use-cases поиска (реализуется app.Service).
type Searcher interface {
	Search(ctx context.Context, q app.Query) (app.Result, error)
	Facets(ctx context.Context, q app.Query) ([]app.Facet, error)
}

// SearchHandler обслуживает публичные эндпоинты поиска объявлений.
type SearchHandler struct {
	svc Searcher
	log *slog.Logger
}

// NewSearchHandler создаёт обработчик поиска.
func NewSearchHandler(svc Searcher, log *slog.Logger) *SearchHandler {
	return &SearchHandler{svc: svc, log: log}
}

type adHitDTO struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	CategoryID  string            `json:"category_id"`
	RegionID    int               `json:"region_id"`
	CityID      *int64            `json:"city_id,omitempty"`
	Price       int64             `json:"price"`
	Currency    string            `json:"currency"`
	Attrs       map[string]string `json:"attrs,omitempty"`
	IsTop       bool              `json:"is_top"`
	CreatedAt   int64             `json:"created_at"`
	BumpedAt    *int64            `json:"bumped_at,omitempty"`
	Location    []float64         `json:"location,omitempty"`
}

type searchResponse struct {
	Hits    []adHitDTO `json:"hits"`
	Found   int        `json:"found"`
	Page    int        `json:"page"`
	PerPage int        `json:"per_page"`
	// Top — топ-блок (featured/TOP над основной выдачей); заглушка до Promotions
	// (CHAPTER 8): непустой лишь на первой странице и когда есть TOP-объявления.
	Top []adHitDTO `json:"top"`
}

type facetValueDTO struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type facetDTO struct {
	Field  string          `json:"field"`
	Values []facetValueDTO `json:"values"`
}

type facetsResponse struct {
	Facets []facetDTO `json:"facets"`
}

// Search обслуживает GET /api/v1/ads/search — полнотекст, фильтры, сортировка,
// пагинация, гео (публично).
func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Search(r.Context(), parseQuery(r.URL.Query()))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toSearchResponse(res))
}

// Facets обслуживает GET /api/v1/ads/search/facets — доступные фасеты по тем же
// фильтрам (публично).
func (h *SearchHandler) Facets(w http.ResponseWriter, r *http.Request) {
	facets, err := h.svc.Facets(r.Context(), parseQuery(r.URL.Query()))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, facetsResponse{Facets: toFacetDTOs(facets)})
}

// parseQuery разбирает query-параметры в доменный запрос. Некорректные числовые
// значения игнорируются (лениво): поиск устойчив к мусорным параметрам.
func parseQuery(q url.Values) app.Query {
	out := app.Query{
		Text:       q.Get("q"),
		CategoryID: q.Get("category_id"),
		RegionID:   atoiDefault(q.Get("region_id"), 0),
		CityID:     atoi64Default(q.Get("city_id"), 0),
		Currency:   q.Get("currency"),
		PriceMin:   parseOptInt64(q.Get("price_min")),
		PriceMax:   parseOptInt64(q.Get("price_max")),
		Sort:       q.Get("sort"),
		Page:       atoiDefault(q.Get("page"), 0),
		PerPage:    atoiDefault(q.Get("per_page"), 0),
		Attrs:      parseAttrs(q),
		AttrFacets: splitComma(q.Get("facets")),
		Geo:        parseGeo(q),
	}
	return out
}

// parseAttrs собирает фильтры по атрибутам из параметров attr.<slug>=value.
func parseAttrs(q url.Values) map[string]string {
	var attrs map[string]string
	for key, vals := range q {
		slug, ok := strings.CutPrefix(key, attrParamPrefix)
		if !ok || slug == "" || len(vals) == 0 || vals[0] == "" {
			continue
		}
		if attrs == nil {
			attrs = make(map[string]string)
		}
		attrs[slug] = vals[0]
	}
	return attrs
}

// parseGeo собирает точку/радиус гео-поиска; возвращает nil при отсутствии
// корректной пары координат.
func parseGeo(q url.Values) *app.Geo {
	lat, okLat := parseFloat(q.Get("lat"))
	lng, okLng := parseFloat(q.Get("lng"))
	if !okLat || !okLng {
		return nil
	}
	radius, _ := parseFloat(q.Get("radius_km"))
	return &app.Geo{Lat: lat, Lng: lng, RadiusKm: radius}
}

func toSearchResponse(res app.Result) searchResponse {
	return searchResponse{
		Hits:    toHitDTOs(res.Hits),
		Found:   res.Found,
		Page:    res.Page,
		PerPage: res.PerPage,
		Top:     toHitDTOs(res.Top),
	}
}

func toHitDTOs(hits []app.AdHit) []adHitDTO {
	out := make([]adHitDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, adHitDTO{
			ID: h.ID, Title: h.Title, Description: h.Description, CategoryID: h.CategoryID,
			RegionID: h.RegionID, CityID: h.CityID, Price: h.Price, Currency: h.Currency,
			Attrs: h.Attrs, IsTop: h.IsTop, CreatedAt: h.CreatedAt, BumpedAt: h.BumpedAt, Location: h.Location,
		})
	}
	return out
}

func toFacetDTOs(facets []app.Facet) []facetDTO {
	out := make([]facetDTO, 0, len(facets))
	for _, f := range facets {
		vals := make([]facetValueDTO, 0, len(f.Values))
		for _, v := range f.Values {
			vals = append(vals, facetValueDTO{Value: v.Value, Count: v.Count})
		}
		out = append(out, facetDTO{Field: f.Field, Values: vals})
	}
	return out
}

// writeError логирует ошибку поиска и отдаёт RFC 7807. Детали Typesense наружу
// не раскрываются.
func (h *SearchHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "ошибка поиска", slog.String("error", err.Error()))
	httpx.WriteProblem(w, r, apperr.New(apperr.KindUnavailable, "search_unavailable",
		"Поиск временно недоступен", "Qidiruv vaqtincha mavjud emas"))
}

// atoiDefault парсит int, возвращая def при пустом/некорректном значении.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// atoi64Default парсит int64, возвращая def при пустом/некорректном значении.
func atoi64Default(s string, def int64) int64 {
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// parseOptInt64 парсит опциональный int64 (nil при пустом/некорректном значении).
func parseOptInt64(s string) *int64 {
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// parseFloat парсит float64; ok=false при пустом/некорректном значении.
func parseFloat(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// splitComma разбивает список через запятую, отбрасывая пустые элементы.
func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}
