// Package app содержит use-cases ручной модерации (Stage 6.3): одобрение,
// отклонение и возврат на доработку задач из ручной очереди.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"bozor/pkg/shared/events"

	"bozor/services/moderation/internal/domain"
)

const source = "moderation"

// Доменные ошибки ручной модерации.
var (
	// ErrTaskNotFound — задача модерации по объявлению не найдена.
	ErrTaskNotFound = errors.New("задача модерации не найдена")
	// ErrNotPending — задача уже решена (не в ручной очереди).
	ErrNotPending = errors.New("задача не в ручной очереди")
)

// Store — доступ к задачам модерации (реализуется repo.Repo).
type Store interface {
	GetTask(ctx context.Context, adID string) (domain.Task, bool, error)
	DecideWithEvent(ctx context.Context, adID, newStatus, moderatorID, comment string, ev events.Envelope) (bool, error)
}

// Service — use-cases ручной модерации.
type Service struct {
	store Store
	log   *slog.Logger
}

// NewService создаёт сервис ручной модерации.
func NewService(store Store, log *slog.Logger) *Service {
	return &Service{store: store, log: log}
}

// approvedPayload — payload bozor.ad.approved (ручное одобрение).
type approvedPayload struct {
	AdID   string `json:"ad_id"`
	UserID string `json:"user_id"`
	Title  string `json:"title"`
}

// rejectedPayload — payload bozor.ad.rejected (отклонение/возврат на доработку).
type rejectedPayload struct {
	AdID        string `json:"ad_id"`
	UserID      string `json:"user_id"`
	Title       string `json:"title"`
	Reason      string `json:"reason"`
	RequestEdit bool   `json:"request_edit"` // true → возврат на доработку (объявление редактируемо)
}

// Approve одобряет объявление из ручной очереди → bozor.ad.approved (→ Listing активирует).
func (s *Service) Approve(ctx context.Context, adID, moderatorID string) error {
	return s.decide(ctx, adID, moderatorID, "", domain.StatusApproved, false)
}

// Reject отклоняет объявление с обязательной причиной → bozor.ad.rejected.
func (s *Service) Reject(ctx context.Context, adID, moderatorID, reason string) error {
	if err := domain.ValidateReason(reason); err != nil {
		return err
	}
	return s.decide(ctx, adID, moderatorID, reason, domain.StatusRejected, false)
}

// RequestEdit возвращает объявление на доработку с обязательным пояснением →
// bozor.ad.rejected (request_edit=true); после правки автором — повторная модерация.
func (s *Service) RequestEdit(ctx context.Context, adID, moderatorID, reason string) error {
	if err := domain.ValidateReason(reason); err != nil {
		return err
	}
	return s.decide(ctx, adID, moderatorID, reason, domain.StatusEditRequested, true)
}

// decide применяет решение к задаче в ручной очереди и публикует событие.
func (s *Service) decide(ctx context.Context, adID, moderatorID, comment, newStatus string, requestEdit bool) error {
	task, found, err := s.store.GetTask(ctx, adID)
	if err != nil {
		return err
	}
	if !found {
		return ErrTaskNotFound
	}
	if task.Status != domain.StatusManual {
		return ErrNotPending
	}

	ev, err := buildEvent(newStatus, task, comment, requestEdit)
	if err != nil {
		return err
	}
	applied, err := s.store.DecideWithEvent(ctx, adID, newStatus, moderatorID, comment, ev)
	if err != nil {
		return err
	}
	if !applied {
		return ErrNotPending // изменилось между чтением и записью
	}

	s.log.InfoContext(ctx, "ручное решение модерации применено",
		slog.String("ad_id", adID), slog.String("status", newStatus),
		slog.String("moderator", moderatorID))
	return nil
}

// buildEvent собирает событие результата модерации по новому статусу задачи.
func buildEvent(newStatus string, t domain.Task, comment string, requestEdit bool) (events.Envelope, error) {
	if newStatus == domain.StatusApproved {
		ev, err := events.New(events.SubjectAdApproved, source, approvedPayload{
			AdID: t.AdID, UserID: t.UserID, Title: t.Title,
		})
		if err != nil {
			return events.Envelope{}, fmt.Errorf("app: сборка bozor.ad.approved: %w", err)
		}
		return ev, nil
	}
	ev, err := events.New(events.SubjectAdRejected, source, rejectedPayload{
		AdID: t.AdID, UserID: t.UserID, Title: t.Title, Reason: comment, RequestEdit: requestEdit,
	})
	if err != nil {
		return events.Envelope{}, fmt.Errorf("app: сборка bozor.ad.rejected: %w", err)
	}
	return ev, nil
}
