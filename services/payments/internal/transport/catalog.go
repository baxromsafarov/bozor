package transport

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/payments/internal/app"
)

// Catalog — use-case каталога (реализуется app.Service).
type Catalog interface {
	GetCatalog(ctx context.Context, regionID *int, categoryID *string) (app.Catalog, error)
}

// CatalogHandler обслуживает публичный каталог платных услуг.
type CatalogHandler struct {
	svc Catalog
	log *slog.Logger
}

// NewCatalogHandler создаёт обработчик каталога.
func NewCatalogHandler(svc Catalog, log *slog.Logger) *CatalogHandler {
	return &CatalogHandler{svc: svc, log: log}
}

type priceOptionDTO struct {
	DurationDays int   `json:"duration_days"`
	AmountUZS    int64 `json:"amount_uzs"`
}

type serviceDTO struct {
	Code          string           `json:"code"`
	NameUZ        string           `json:"name_uz"`
	NameRU        string           `json:"name_ru"`
	DescriptionUZ string           `json:"description_uz"`
	DescriptionRU string           `json:"description_ru"`
	Options       []priceOptionDTO `json:"options"`
}

type bundleItemDTO struct {
	ServiceCode  string `json:"service_code"`
	DurationDays int    `json:"duration_days"`
	BumpSchedule []int  `json:"bump_schedule_days"`
}

type bundleDTO struct {
	Code          string          `json:"code"`
	NameUZ        string          `json:"name_uz"`
	NameRU        string          `json:"name_ru"`
	DescriptionUZ string          `json:"description_uz"`
	DescriptionRU string          `json:"description_ru"`
	AmountUZS     *int64          `json:"amount_uzs"`
	Items         []bundleItemDTO `json:"items"`
}

type catalogDTO struct {
	Currency   string       `json:"currency"`
	RegionID   *int         `json:"region_id"`
	CategoryID *string      `json:"category_id"`
	Services   []serviceDTO `json:"services"`
	Bundles    []bundleDTO  `json:"bundles"`
}

// Get отдаёт каталог услуг/наборов с ценами. Необязательные query-параметры
// region_id (положительное число) и category_id (UUID) уточняют прайс; без них —
// базовые (общестрановые) цены.
func (h *CatalogHandler) Get(w http.ResponseWriter, r *http.Request) {
	regionID, err := parseRegion(r.URL.Query().Get("region_id"))
	if err != nil {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_region_id",
			"Некорректный идентификатор региона", "Yaroqsiz hudud identifikatori"))
		return
	}
	categoryID, err := parseCategory(r.URL.Query().Get("category_id"))
	if err != nil {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_category_id",
			"Некорректный идентификатор категории", "Yaroqsiz turkum identifikatori"))
		return
	}

	cat, err := h.svc.GetCatalog(r.Context(), regionID, categoryID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "каталог услуг", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "catalog_failed",
			"Не удалось получить каталог услуг", "Xizmatlar katalogini olib bo'lmadi"))
		return
	}

	httpx.Respond(w, http.StatusOK, toCatalogDTO(cat))
}

// parseRegion разбирает необязательный region_id: пусто → nil, иначе > 0.
func parseRegion(raw string) (*int, error) {
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return nil, errInvalid
	}
	return &v, nil
}

// parseCategory разбирает необязательный category_id: пусто → nil, иначе UUID.
func parseCategory(raw string) (*string, error) {
	if raw == "" {
		return nil, nil
	}
	if _, err := uuid.Parse(raw); err != nil {
		return nil, errInvalid
	}
	return &raw, nil
}

var errInvalid = apperr.New(apperr.KindInvalid, "invalid", "", "")

func toCatalogDTO(cat app.Catalog) catalogDTO {
	out := catalogDTO{
		Currency:   cat.Currency,
		RegionID:   cat.RegionID,
		CategoryID: cat.CategoryID,
		Services:   make([]serviceDTO, len(cat.Services)),
		Bundles:    make([]bundleDTO, len(cat.Bundles)),
	}
	for i, s := range cat.Services {
		opts := make([]priceOptionDTO, len(s.Options))
		for j, o := range s.Options {
			opts[j] = priceOptionDTO{DurationDays: o.Duration, AmountUZS: o.AmountUZS}
		}
		out.Services[i] = serviceDTO{
			Code: s.Code, NameUZ: s.NameUZ, NameRU: s.NameRU,
			DescriptionUZ: s.DescriptionUZ, DescriptionRU: s.DescriptionRU,
			Options: opts,
		}
	}
	for i, b := range cat.Bundles {
		items := make([]bundleItemDTO, len(b.Items))
		for j, it := range b.Items {
			sched := it.BumpSchedule
			if sched == nil {
				sched = []int{}
			}
			items[j] = bundleItemDTO{ServiceCode: it.ServiceCode, DurationDays: it.Duration, BumpSchedule: sched}
		}
		dto := bundleDTO{
			Code: b.Code, NameUZ: b.NameUZ, NameRU: b.NameRU,
			DescriptionUZ: b.DescriptionUZ, DescriptionRU: b.DescriptionRU,
			Items: items,
		}
		if b.Priced {
			amount := b.AmountUZS
			dto.AmountUZS = &amount
		}
		out.Bundles[i] = dto
	}
	return out
}
