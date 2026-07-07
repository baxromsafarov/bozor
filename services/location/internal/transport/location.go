package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/location/internal/domain"
)

// Service — use-cases справочника (реализуется app.Service).
type Service interface {
	Regions(ctx context.Context) ([]domain.Region, error)
	Cities(ctx context.Context, regionID int) ([]domain.City, error)
}

// Handler обслуживает справочные эндпоинты регионов и городов.
type Handler struct {
	svc Service
	log *slog.Logger
}

// NewHandler создаёт обработчик справочника.
func NewHandler(svc Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

type regionResponse struct {
	ID        int      `json:"id"`
	Slug      string   `json:"slug"`
	NameUZ    string   `json:"name_uz"`
	NameRU    string   `json:"name_ru"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

type cityResponse struct {
	ID        int      `json:"id"`
	RegionID  int      `json:"region_id"`
	Slug      string   `json:"slug"`
	NameUZ    string   `json:"name_uz"`
	NameRU    string   `json:"name_ru"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

// Regions отдаёт список всех регионов.
func (h *Handler) Regions(w http.ResponseWriter, r *http.Request) {
	regions, err := h.svc.Regions(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "запрос регионов", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "regions_failed",
			"Не удалось получить регионы", "Hududlarni olib bo'lmadi"))
		return
	}
	out := make([]regionResponse, len(regions))
	for i, reg := range regions {
		out[i] = regionResponse{
			ID: reg.ID, Slug: reg.Slug, NameUZ: reg.NameUZ, NameRU: reg.NameRU,
			Latitude: reg.Latitude, Longitude: reg.Longitude,
		}
	}
	httpx.Respond(w, http.StatusOK, map[string]any{"regions": out})
}

// Cities отдаёт города региона; несуществующий регион → 404.
func (h *Handler) Cities(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "regionID"))
	if err != nil || id <= 0 {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_region_id",
			"Некорректный идентификатор региона", "Yaroqsiz hudud identifikatori"))
		return
	}

	cities, err := h.svc.Cities(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrRegionNotFound) {
			httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "region_not_found",
				"Регион не найден", "Hudud topilmadi"))
			return
		}
		h.log.ErrorContext(r.Context(), "запрос городов", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "cities_failed",
			"Не удалось получить города", "Shaharlarni olib bo'lmadi"))
		return
	}

	out := make([]cityResponse, len(cities))
	for i, c := range cities {
		out[i] = cityResponse{
			ID: c.ID, RegionID: c.RegionID, Slug: c.Slug, NameUZ: c.NameUZ, NameRU: c.NameRU,
			Latitude: c.Latitude, Longitude: c.Longitude,
		}
	}
	httpx.Respond(w, http.StatusOK, map[string]any{"cities": out})
}
