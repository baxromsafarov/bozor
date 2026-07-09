package transport

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/httpx"

	"bozor/services/listing/internal/domain"
)

// Внутренние read-эндпоинты (не под /api/v1, не проксируются gateway) отдают
// полную проекцию объявления для синхронизации read-модели Search (Stage 4.2).
// Просмотр не учитывается (в отличие от публичного GET /ads/{id}).

// exportAd — полная проекция объявления для индексатора Search.
type exportAd struct {
	ID          string         `json:"id"`
	UserID      string         `json:"user_id"`
	CategoryID  string         `json:"category_id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Price       int64          `json:"price"`
	Currency    string         `json:"currency"`
	RegionID    int16          `json:"region_id"`
	CityID      *int64         `json:"city_id,omitempty"`
	Lat         *float64       `json:"lat,omitempty"`
	Lng         *float64       `json:"lng,omitempty"`
	Status      string         `json:"status"`
	ViewsCount  int64          `json:"views_count"`
	Attributes  []attributeDTO `json:"attributes"`
	CreatedAt   string         `json:"created_at"`
	PublishedAt string         `json:"published_at,omitempty"`
	ExpiresAt   string         `json:"expires_at,omitempty"`
	BumpedAt    string         `json:"bumped_at,omitempty"`
}

type exportListResponse struct {
	Ads       []exportAd `json:"ads"`
	NextAfter string     `json:"next_after"`
}

// ExportGet отдаёт полное объявление по id для внутренней синхронизации.
func (h *Handler) ExportGet(w http.ResponseWriter, r *http.Request) {
	ad, err := h.svc.ExportByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writeAdError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toExport(ad))
}

// ExportList отдаёт страницу активных объявлений (keyset по id) для переиндексации.
func (h *Handler) ExportList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := pageParams(q)
	ads, err := h.svc.ExportActive(r.Context(), q.Get("after"), limit)
	if err != nil {
		h.writeAdError(w, r, err)
		return
	}
	resp := exportListResponse{Ads: make([]exportAd, 0, len(ads))}
	for _, a := range ads {
		resp.Ads = append(resp.Ads, toExport(a))
	}
	if n := len(resp.Ads); n > 0 {
		resp.NextAfter = resp.Ads[n-1].ID
	}
	httpx.Respond(w, http.StatusOK, resp)
}

// toExport строит полную проекцию объявления.
func toExport(a domain.Ad) exportAd {
	e := exportAd{
		ID: a.ID, UserID: a.UserID, CategoryID: a.CategoryID, Title: a.Title, Description: a.Description,
		Price: a.Price, Currency: a.Currency, RegionID: a.RegionID, CityID: a.CityID, Lat: a.Lat, Lng: a.Lng,
		Status: string(a.Status), ViewsCount: a.ViewsCount,
		Attributes:  make([]attributeDTO, 0, len(a.Attributes)),
		CreatedAt:   a.CreatedAt.UTC().Format(time.RFC3339),
		PublishedAt: formatTime(a.PublishedAt),
		ExpiresAt:   formatTime(a.ExpiresAt),
		BumpedAt:    formatTime(a.BumpedAt),
	}
	for _, v := range a.Attributes {
		e.Attributes = append(e.Attributes, attributeDTO{Slug: v.AttributeSlug, Value: v.Value})
	}
	return e
}
