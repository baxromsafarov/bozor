// Package transport собирает HTTP-слой Moderation-сервиса. Очередь модерации
// доступна только персоналу (admin/moderator из проброшенной gateway идентичности).
package transport

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/moderation/internal/domain"
)

// defaultLimit / maxLimit — размер страницы очереди.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// allowedStatuses — статусы, по которым можно фильтровать очередь.
var allowedStatuses = map[string]bool{
	domain.StatusManual: true, domain.StatusApproved: true,
	domain.StatusRejected: true, domain.StatusEditRequested: true,
}

// Reader — чтение задач модерации (реализуется repo.Repo).
type Reader interface {
	ListTasks(ctx context.Context, status string, limit int) ([]domain.Task, error)
}

// Handler обслуживает очередь модерации.
type Handler struct {
	reader Reader
	log    *slog.Logger
}

// NewHandler создаёт обработчик очереди модерации.
func NewHandler(reader Reader, log *slog.Logger) *Handler {
	return &Handler{reader: reader, log: log}
}

type taskDTO struct {
	ID         string   `json:"id"`
	AdID       string   `json:"ad_id"`
	UserID     string   `json:"user_id"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	AutoResult string   `json:"auto_result"`
	Reasons    []string `json:"reasons"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

type listResponse struct {
	Tasks  []taskDTO `json:"tasks"`
	Status string    `json:"status"`
	Limit  int       `json:"limit"`
}

// ListTasks отдаёт задачи модерации по статусу (по умолчанию — ручная очередь).
func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = domain.StatusManual
	}
	if !allowedStatuses[status] {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_status",
			"Недопустимый статус", "Yaroqsiz holat"))
		return
	}

	limit := clampLimit(r.URL.Query().Get("limit"))
	tasks, err := h.reader.ListTasks(r.Context(), status, limit)
	if err != nil {
		h.log.ErrorContext(r.Context(), "ошибка чтения очереди", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "moderation_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
		return
	}

	httpx.Respond(w, http.StatusOK, toListResponse(tasks, status, limit))
}

func toListResponse(tasks []domain.Task, status string, limit int) listResponse {
	out := listResponse{Tasks: make([]taskDTO, 0, len(tasks)), Status: status, Limit: limit}
	for _, t := range tasks {
		reasons := t.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		out.Tasks = append(out.Tasks, taskDTO{
			ID: t.ID, AdID: t.AdID, UserID: t.UserID, Title: t.Title,
			Status: t.Status, AutoResult: t.AutoResult, Reasons: reasons,
			CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func clampLimit(s string) int {
	if s == "" {
		return defaultLimit
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}
