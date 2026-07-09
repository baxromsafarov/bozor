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

	"bozor/services/moderation/internal/app"
	"bozor/services/moderation/internal/domain"
)

// Ops — use-cases жалоб/банов (реализуется app.OpsService).
type Ops interface {
	CreateReport(ctx context.Context, reporterID, targetType, targetID, reason string) (domain.Report, error)
	ListReports(ctx context.Context, status string, limit int) ([]domain.Report, error)
	ResolveReport(ctx context.Context, reportID, moderatorID, action, note string) error
	BanUser(ctx context.Context, moderatorID, userID, banType, reason string, duration time.Duration) (domain.Ban, error)
}

// ReportHandler обслуживает подачу жалоб (пользователь) и разбор/баны (модератор).
type ReportHandler struct {
	ops Ops
	log *slog.Logger
}

// NewReportHandler создаёт обработчик жалоб/банов.
func NewReportHandler(ops Ops, log *slog.Logger) *ReportHandler {
	return &ReportHandler{ops: ops, log: log}
}

type createReportRequest struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason"`
}

type reportDTO struct {
	ID         string `json:"id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type resolveRequest struct {
	Action string `json:"action"`
	Note   string `json:"note"`
}

type banRequest struct {
	UserID          string `json:"user_id"`
	Type            string `json:"type"`
	Reason          string `json:"reason"`
	DurationSeconds int64  `json:"duration_seconds"`
}

type banDTO struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	Type      string  `json:"type"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

// Create подаёт жалобу от текущего пользователя.
func (h *ReportHandler) Create(w http.ResponseWriter, r *http.Request) {
	reporter := authx.UserID(r.Context())
	if reporter == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
			"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
		return
	}
	var req createReportRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	rep, err := h.ops.CreateReport(r.Context(), reporter, req.TargetType, req.TargetID, req.Reason)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toReportDTO(rep))
}

// List отдаёт очередь жалоб по статусу (по умолчанию open) — только персонал.
func (h *ReportHandler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = domain.ReportOpen
	}
	reports, err := h.ops.ListReports(r.Context(), status, clampLimit(r.URL.Query().Get("limit")))
	if err != nil {
		h.log.ErrorContext(r.Context(), "ошибка чтения жалоб", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "moderation_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
		return
	}
	out := make([]reportDTO, 0, len(reports))
	for _, rep := range reports {
		out = append(out, toReportDTO(rep))
	}
	httpx.Respond(w, http.StatusOK, map[string]any{"reports": out, "status": status})
}

// Resolve разбирает жалобу действием модератора — только персонал.
func (h *ReportHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	var req resolveRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	err := h.ops.ResolveReport(r.Context(), chi.URLParam(r, "id"), authx.UserID(r.Context()), req.Action, req.Note)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, map[string]string{"id": chi.URLParam(r, "id"), "action": req.Action})
}

// Ban банит пользователя — только персонал.
func (h *ReportHandler) Ban(w http.ResponseWriter, r *http.Request) {
	var req banRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.UserID == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_user_id",
			"Требуется идентификатор пользователя", "Foydalanuvchi identifikatori talab qilinadi"))
		return
	}
	ban, err := h.ops.BanUser(r.Context(), authx.UserID(r.Context()), req.UserID, req.Type, req.Reason,
		time.Duration(req.DurationSeconds)*time.Second)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toBanDTO(ban))
}

func toReportDTO(rep domain.Report) reportDTO {
	return reportDTO{
		ID: rep.ID, TargetType: rep.TargetType, TargetID: rep.TargetID,
		Status: rep.Status, Reason: rep.Reason, CreatedAt: rep.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func toBanDTO(ban domain.Ban) banDTO {
	dto := banDTO{ID: ban.ID, UserID: ban.UserID, Type: ban.Type}
	if ban.ExpiresAt != nil {
		s := ban.ExpiresAt.UTC().Format(time.RFC3339)
		dto.ExpiresAt = &s
	}
	return dto
}

// writeError переводит доменные ошибки жалоб/банов в ответы RFC 7807.
func (h *ReportHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidTarget):
		writeInvalid(w, r, "invalid_target", "Некорректный объект жалобы", "Shikoyat obyekti noto'g'ri")
	case errors.Is(err, domain.ErrInvalidAction):
		writeInvalid(w, r, "invalid_action", "Недопустимое действие", "Yaroqsiz amal")
	case errors.Is(err, domain.ErrTakedownTarget):
		writeInvalid(w, r, "takedown_target", "Снятие применимо только к объявлению", "O'chirish faqat e'longa tegishli")
	case errors.Is(err, domain.ErrReasonRequired):
		writeInvalid(w, r, "reason_required", "Требуется причина", "Sabab talab qilinadi")
	case errors.Is(err, domain.ErrReasonTooLong):
		writeInvalid(w, r, "reason_too_long", "Причина слишком длинная", "Sabab juda uzun")
	case errors.Is(err, domain.ErrInvalidBanType):
		writeInvalid(w, r, "invalid_ban_type", "Недопустимый тип бана", "Yaroqsiz ban turi")
	case errors.Is(err, domain.ErrBanDurationRequired):
		writeInvalid(w, r, "ban_duration_required", "Требуется срок бана", "Ban muddati talab qilinadi")
	case errors.Is(err, app.ErrReportNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "report_not_found",
			"Жалоба не найдена", "Shikoyat topilmadi"))
	case errors.Is(err, app.ErrReportClosed):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "report_closed",
			"Жалоба уже закрыта", "Shikoyat allaqachon yopilgan"))
	default:
		h.log.ErrorContext(r.Context(), "ошибка жалоб/банов", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "moderation_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
	}
}

func writeInvalid(w http.ResponseWriter, r *http.Request, code, ru, uz string) {
	httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, code, ru, uz))
}
