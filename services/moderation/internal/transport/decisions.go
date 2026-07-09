package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/moderation/internal/app"
	"bozor/services/moderation/internal/domain"
)

// maxBodyBytes — предел тела запроса решения.
const maxBodyBytes = 8 << 10

// Decider — ручные действия модератора (реализуется app.Service).
type Decider interface {
	Approve(ctx context.Context, adID, moderatorID string) error
	Reject(ctx context.Context, adID, moderatorID, reason string) error
	RequestEdit(ctx context.Context, adID, moderatorID, reason string) error
}

// DecisionHandler обслуживает ручные действия модератора над задачами очереди.
type DecisionHandler struct {
	svc Decider
	log *slog.Logger
}

// NewDecisionHandler создаёт обработчик ручных решений.
func NewDecisionHandler(svc Decider, log *slog.Logger) *DecisionHandler {
	return &DecisionHandler{svc: svc, log: log}
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

type decisionResponse struct {
	AdID   string `json:"ad_id"`
	Action string `json:"action"`
}

// Approve одобряет объявление из ручной очереди.
func (h *DecisionHandler) Approve(w http.ResponseWriter, r *http.Request) {
	adID := chi.URLParam(r, "adId")
	if err := h.svc.Approve(r.Context(), adID, authx.UserID(r.Context())); err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, decisionResponse{AdID: adID, Action: "approved"})
}

// Reject отклоняет объявление с обязательной причиной.
func (h *DecisionHandler) Reject(w http.ResponseWriter, r *http.Request) {
	adID := chi.URLParam(r, "adId")
	reason, ok := h.decodeReason(w, r)
	if !ok {
		return
	}
	if err := h.svc.Reject(r.Context(), adID, authx.UserID(r.Context()), reason); err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, decisionResponse{AdID: adID, Action: "rejected"})
}

// RequestEdit возвращает объявление на доработку с обязательным пояснением.
func (h *DecisionHandler) RequestEdit(w http.ResponseWriter, r *http.Request) {
	adID := chi.URLParam(r, "adId")
	reason, ok := h.decodeReason(w, r)
	if !ok {
		return
	}
	if err := h.svc.RequestEdit(r.Context(), adID, authx.UserID(r.Context()), reason); err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, decisionResponse{AdID: adID, Action: "edit_requested"})
}

func (h *DecisionHandler) decodeReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req reasonRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return "", false
	}
	return req.Reason, true
}

// writeError переводит доменные ошибки решений в ответы RFC 7807.
func (h *DecisionHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, app.ErrTaskNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "task_not_found",
			"Задача модерации не найдена", "Moderatsiya vazifasi topilmadi"))
	case errors.Is(err, app.ErrNotPending):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "task_not_pending",
			"Задача уже обработана", "Vazifa allaqachon ko'rib chiqilgan"))
	case errors.Is(err, domain.ErrReasonRequired):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "reason_required",
			"Требуется причина", "Sabab talab qilinadi"))
	case errors.Is(err, domain.ErrReasonTooLong):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "reason_too_long",
			"Причина слишком длинная", "Sabab juda uzun"))
	default:
		h.log.ErrorContext(r.Context(), "ошибка решения модерации", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "moderation_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
	}
}
