package transport

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/favorites-savedsearch/internal/app"
	"bozor/services/favorites-savedsearch/internal/domain"
)

// maxBodyBytes — предел тела запросов записи сохранённых поисков.
const maxBodyBytes = 16 << 10

// SavedSearchService — use-cases сохранённых поисков (реализуется app.SavedSearchService).
type SavedSearchService interface {
	Create(ctx context.Context, userID string, in app.CreateSavedSearchInput) (domain.SavedSearch, error)
	List(ctx context.Context, userID string) ([]domain.SavedSearch, error)
	Delete(ctx context.Context, id, userID string) error
}

// SavedSearchHandler обслуживает эндпоинты сохранённых поисков.
type SavedSearchHandler struct {
	svc SavedSearchService
}

// NewSavedSearchHandler создаёт обработчик сохранённых поисков.
func NewSavedSearchHandler(svc SavedSearchService) *SavedSearchHandler {
	return &SavedSearchHandler{svc: svc}
}

type queryDTO struct {
	Text       string            `json:"text,omitempty"`
	CategoryID string            `json:"category_id,omitempty"`
	RegionID   int16             `json:"region_id,omitempty"`
	CityID     int64             `json:"city_id,omitempty"`
	PriceMin   *int64            `json:"price_min,omitempty"`
	PriceMax   *int64            `json:"price_max,omitempty"`
	Currency   string            `json:"currency,omitempty"`
	Attrs      map[string]string `json:"attrs,omitempty"`
}

type createSavedSearchRequest struct {
	Name          string   `json:"name"`
	Query         queryDTO `json:"query"`
	NotifyEnabled *bool    `json:"notify_enabled"`
}

type savedSearchResponse struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Query         queryDTO `json:"query"`
	NotifyEnabled bool     `json:"notify_enabled"`
	CreatedAt     string   `json:"created_at"`
}

type savedSearchListResponse struct {
	SavedSearches []savedSearchResponse `json:"saved_searches"`
}

// Create создаёт сохранённый поиск владельца.
func (h *SavedSearchHandler) Create(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		unauthorized(w, r)
		return
	}
	var req createSavedSearchRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	notify := true // по умолчанию уведомления включены
	if req.NotifyEnabled != nil {
		notify = *req.NotifyEnabled
	}
	ss, err := h.svc.Create(r.Context(), owner, app.CreateSavedSearchInput{
		Name: req.Name, Query: toQuery(req.Query), NotifyEnabled: notify,
	})
	if err != nil {
		writeSavedSearchError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toSavedSearchResponse(ss))
}

// List отдаёт сохранённые поиски владельца.
func (h *SavedSearchHandler) List(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		unauthorized(w, r)
		return
	}
	list, err := h.svc.List(r.Context(), owner)
	if err != nil {
		writeSavedSearchError(w, r, err)
		return
	}
	out := savedSearchListResponse{SavedSearches: make([]savedSearchResponse, 0, len(list))}
	for _, ss := range list {
		out.SavedSearches = append(out.SavedSearches, toSavedSearchResponse(ss))
	}
	httpx.Respond(w, http.StatusOK, out)
}

// Delete удаляет сохранённый поиск владельца.
func (h *SavedSearchHandler) Delete(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		unauthorized(w, r)
		return
	}
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id"), owner); err != nil {
		writeSavedSearchError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toQuery(q queryDTO) domain.SearchQuery {
	return domain.SearchQuery{
		Text: q.Text, CategoryID: q.CategoryID, RegionID: q.RegionID, CityID: q.CityID,
		PriceMin: q.PriceMin, PriceMax: q.PriceMax, Currency: q.Currency, Attrs: q.Attrs,
	}
}

func toSavedSearchResponse(ss domain.SavedSearch) savedSearchResponse {
	return savedSearchResponse{
		ID:   ss.ID,
		Name: ss.Name,
		Query: queryDTO{
			Text: ss.Query.Text, CategoryID: ss.Query.CategoryID, RegionID: ss.Query.RegionID,
			CityID: ss.Query.CityID, PriceMin: ss.Query.PriceMin, PriceMax: ss.Query.PriceMax,
			Currency: ss.Query.Currency, Attrs: ss.Query.Attrs,
		},
		NotifyEnabled: ss.NotifyEnabled,
		CreatedAt:     ss.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
		"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
}

// writeSavedSearchError переводит доменные ошибки в ответы RFC 7807.
func writeSavedSearchError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrSavedSearchNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "saved_search_not_found",
			"Сохранённый поиск не найден", "Saqlangan qidiruv topilmadi"))
	case errors.Is(err, domain.ErrEmptyName), errors.Is(err, domain.ErrNameTooLong):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_saved_search_name",
			"Некорректное имя сохранённого поиска", "Saqlangan qidiruv nomi noto'g'ri"))
	case errors.Is(err, domain.ErrTooManySavedSearches):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "too_many_saved_searches",
			"Превышен лимит сохранённых поисков", "Saqlangan qidiruvlar chegarasi oshib ketdi"))
	default:
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "saved_search_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
	}
}
