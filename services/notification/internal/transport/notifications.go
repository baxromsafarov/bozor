// Package transport собирает HTTP-слой Notification-сервиса: история уведомлений
// владельца со статусами доставки (только чтение; отправка — событийная).
package transport

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/notification/internal/domain"
)

// defaultLimit / maxLimit — размер страницы истории уведомлений.
const (
	defaultLimit = 30
	maxLimit     = 100
)

// Reader — чтение истории уведомлений (реализуется repo.Repo).
type Reader interface {
	ListByUser(ctx context.Context, userID string, limit int) ([]domain.Notification, error)
}

// Handler обслуживает историю уведомлений владельца.
type Handler struct {
	reader Reader
	log    *slog.Logger
}

// NewHandler создаёт обработчик истории уведомлений.
func NewHandler(reader Reader, log *slog.Logger) *Handler {
	return &Handler{reader: reader, log: log}
}

type notificationDTO struct {
	ID        string  `json:"id"`
	EventType string  `json:"event_type"`
	Channel   string  `json:"channel"`
	Body      string  `json:"body"`
	Status    string  `json:"status"`
	Reason    string  `json:"reason,omitempty"`
	Attempts  int     `json:"attempts"`
	CreatedAt string  `json:"created_at"`
	SentAt    *string `json:"sent_at,omitempty"`
}

type listResponse struct {
	Notifications []notificationDTO `json:"notifications"`
	Limit         int               `json:"limit"`
}

// List отдаёт последние уведомления владельца (из проброшенной идентичности).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
			"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
		return
	}

	limit := clampLimit(r.URL.Query().Get("limit"))
	items, err := h.reader.ListByUser(r.Context(), owner, limit)
	if err != nil {
		h.log.ErrorContext(r.Context(), "ошибка чтения уведомлений", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "notifications_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
		return
	}

	httpx.Respond(w, http.StatusOK, toListResponse(items, limit))
}

func toListResponse(items []domain.Notification, limit int) listResponse {
	out := listResponse{Notifications: make([]notificationDTO, 0, len(items)), Limit: limit}
	for _, n := range items {
		dto := notificationDTO{
			ID: n.ID, EventType: n.EventType, Channel: n.Channel, Body: n.Body,
			Status: n.Status, Reason: n.Reason, Attempts: n.Attempts,
			CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339),
		}
		if n.SentAt != nil {
			s := n.SentAt.UTC().Format(time.RFC3339)
			dto.SentAt = &s
		}
		out.Notifications = append(out.Notifications, dto)
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
