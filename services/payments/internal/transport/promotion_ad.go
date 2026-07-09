package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/payments/internal/app"
	"bozor/services/payments/internal/domain"
)

const maxPromoteBody = 4 << 10

// Promotions — use-cases применения услуг (реализуется app.PromotionService).
type Promotions interface {
	Promote(ctx context.Context, userID, adID string, req app.PromoteRequest) ([]domain.AdPromotion, error)
	Promotions(ctx context.Context, adID string) ([]domain.AdPromotion, error)
}

// PromotionHandler обслуживает продвижение объявлений.
type PromotionHandler struct {
	svc Promotions
	log *slog.Logger
}

// NewPromotionHandler создаёт обработчик продвижения.
func NewPromotionHandler(svc Promotions, log *slog.Logger) *PromotionHandler {
	return &PromotionHandler{svc: svc, log: log}
}

type promoteRequest struct {
	ServiceCode  string `json:"service_code"`
	DurationDays int    `json:"duration_days"`
	BundleCode   string `json:"bundle_code"`
}

type promotionDTO struct {
	ID          string     `json:"id"`
	AdID        string     `json:"ad_id"`
	ServiceCode string     `json:"service_code"`
	BundleCode  *string    `json:"bundle_code"`
	Status      string     `json:"status"`
	AmountUZS   int64      `json:"amount_uzs"`
	StartsAt    time.Time  `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	Schedule    []int      `json:"schedule_days,omitempty"`
}

// Promote применяет услугу/набор к объявлению текущего пользователя.
func (h *PromotionHandler) Promote(w http.ResponseWriter, r *http.Request) {
	userID := authx.UserID(r.Context())
	adID := chi.URLParam(r, "adID")

	var req promoteRequest
	if err := httpx.DecodeJSON(w, r, &req, maxPromoteBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	promos, err := h.svc.Promote(r.Context(), userID, adID, app.PromoteRequest{
		ServiceCode: req.ServiceCode, DurationDays: req.DurationDays, BundleCode: req.BundleCode,
	})
	if err != nil {
		writePromoteError(w, r, h.log, err)
		return
	}

	httpx.Respond(w, http.StatusCreated, map[string]any{"promotions": toPromotionDTOs(promos)})
}

// List отдаёт активные услуги объявления.
func (h *PromotionHandler) List(w http.ResponseWriter, r *http.Request) {
	adID := chi.URLParam(r, "adID")

	promos, err := h.svc.Promotions(r.Context(), adID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "список услуг объявления", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "promotions_failed",
			"Не удалось получить услуги", "Xizmatlarni olib bo'lmadi"))
		return
	}
	httpx.Respond(w, http.StatusOK, map[string]any{"promotions": toPromotionDTOs(promos)})
}

func writePromoteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, domain.ErrAdNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "ad_not_found",
			"Объявление не найдено", "E'lon topilmadi"))
	case errors.Is(err, domain.ErrNotAdOwner):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindForbidden, "not_ad_owner",
			"Объявление принадлежит другому пользователю", "E'lon boshqa foydalanuvchiga tegishli"))
	case errors.Is(err, domain.ErrAdNotPromotable):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "ad_not_promotable",
			"Объявление нельзя продвигать в текущем статусе", "E'lonni hozirgi holatda targ'ib qilib bo'lmaydi"))
	case errors.Is(err, domain.ErrEmptyPromotion):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "empty_promotion",
			"Укажите услугу или набор", "Xizmat yoki to'plamni ko'rsating"))
	case errors.Is(err, domain.ErrUnknownService), errors.Is(err, domain.ErrUnknownBundle),
		errors.Is(err, domain.ErrInvalidDuration), errors.Is(err, domain.ErrNoPrice):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_promotion",
			"Некорректная услуга или длительность", "Yaroqsiz xizmat yoki muddat"))
	case errors.Is(err, domain.ErrInsufficientFunds):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "insufficient_funds",
			"Недостаточно средств на кошельке", "Hamyonda mablag' yetarli emas"))
	default:
		log.ErrorContext(r.Context(), "продвижение объявления", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "promote_failed",
			"Не удалось применить услугу", "Xizmatni qo'llab bo'lmadi"))
	}
}

func toPromotionDTOs(promos []domain.AdPromotion) []promotionDTO {
	out := make([]promotionDTO, len(promos))
	for i, p := range promos {
		out[i] = promotionDTO{
			ID: p.ID, AdID: p.AdID, ServiceCode: p.ServiceCode, BundleCode: p.BundleCode,
			Status: p.Status, AmountUZS: p.AmountUZS, StartsAt: p.StartsAt, EndsAt: p.EndsAt,
			Schedule: p.Schedule,
		}
	}
	return out
}
