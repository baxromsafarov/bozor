// Package worker содержит фоновые процессы Listing-сервиса: потребление решений
// модерации (bozor.ad.approved|rejected) и перевод объявлений в expired по сроку.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/listing/internal/app"
	"bozor/services/listing/internal/domain"
)

// ModerationConsumer — имя durable-консьюмера и ключ inbox-идемпотентности.
const ModerationConsumer = "listing-moderation"

const source = "listing"

// ModerationStore — операции БД, нужные обработчику решений модерации.
type ModerationStore interface {
	IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, consumer, eventID string) error
	GetByID(ctx context.Context, id string) (domain.Ad, error)
	ApplyModerationWithEvent(ctx context.Context, consumer, eventID, adID string, upd domain.StatusUpdate, ev events.Envelope) error
}

// Moderator потребляет решения модерации и переводит объявление pending → active
// (одобрено: проставляются published_at и expires_at = now + adTTL) либо
// pending → rejected (отклонено). Публикует bozor.ad.updated (источник истины).
type Moderator struct {
	store ModerationStore
	adTTL time.Duration
	log   *slog.Logger
}

// NewModerator создаёт обработчик решений модерации.
func NewModerator(store ModerationStore, adTTL time.Duration, log *slog.Logger) *Moderator {
	return &Moderator{store: store, adTTL: adTTL, log: log}
}

// decisionPayload — интересующая часть решения модерации (bozor.ad.approved|rejected).
type decisionPayload struct {
	AdID string `json:"ad_id"`
}

// Handle обрабатывает одно решение модерации. Ошибка ведёт к повтору/DLQ (natsx),
// nil — к подтверждению. Идемпотентно: inbox + условный переход (см. plan) —
// повторная доставка и события «не в модерации» не выполняют работу дважды.
func (m *Moderator) Handle(ctx context.Context, env events.Envelope) error {
	var pl decisionPayload
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор решения модерации: %w", err)
	}
	if pl.AdID == "" {
		return errors.New("worker: пустой ad_id в решении модерации")
	}

	processed, err := m.store.IsEventProcessed(ctx, ModerationConsumer, env.ID)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}

	ad, err := m.store.GetByID(ctx, pl.AdID)
	if errors.Is(err, domain.ErrAdNotFound) {
		// Объявление удалено до применения решения — отметить событие и выйти.
		return m.store.MarkEventProcessed(ctx, ModerationConsumer, env.ID)
	}
	if err != nil {
		return err
	}

	upd, ok, err := m.plan(env.Type, ad.Status)
	if err != nil {
		return err
	}
	if !ok {
		// Не в модерации / уже в целевом статусе — работа не нужна, но событие отметим.
		return m.store.MarkEventProcessed(ctx, ModerationConsumer, env.ID)
	}

	// Отражаем переход в копии для полезной нагрузки события источника истины.
	ad.Status = upd.To
	if upd.PublishedAt != nil {
		ad.PublishedAt = upd.PublishedAt
	}
	if upd.ExpiresAt != nil {
		ad.ExpiresAt = upd.ExpiresAt
	}
	ev, err := events.New(events.SubjectAdUpdated, source, app.NewAdEvent(ad))
	if err != nil {
		return fmt.Errorf("worker: сборка события: %w", err)
	}

	m.log.InfoContext(ctx, "решение модерации применено",
		slog.String("ad_id", ad.ID), slog.String("status", string(upd.To)))
	return m.store.ApplyModerationWithEvent(ctx, ModerationConsumer, env.ID, ad.ID, upd, ev)
}

// plan строит переход из типа события и текущего статуса объявления.
// approved/rejected — решения модерации подачи: применяются только из pending
// (approved → active с published_at и expires_at = now + adTTL; rejected → rejected).
// blocked — снятие по жалобе: любой статус → blocked (кроме уже заблокированного).
// ok=false — работать не нужно (объявление не в модерации либо уже снято): событие
// лишь отмечается обработанным.
func (m *Moderator) plan(eventType string, current domain.Status) (domain.StatusUpdate, bool, error) {
	switch eventType {
	case events.SubjectAdApproved:
		if current != domain.StatusPending {
			return domain.StatusUpdate{}, false, nil
		}
		now := time.Now().UTC()
		exp := now.Add(m.adTTL)
		return domain.StatusUpdate{From: domain.StatusPending, To: domain.StatusActive, PublishedAt: &now, ExpiresAt: &exp}, true, nil
	case events.SubjectAdRejected:
		if current != domain.StatusPending {
			return domain.StatusUpdate{}, false, nil
		}
		return domain.StatusUpdate{From: domain.StatusPending, To: domain.StatusRejected}, true, nil
	case events.SubjectAdBlocked:
		if current == domain.StatusBlocked {
			return domain.StatusUpdate{}, false, nil
		}
		return domain.StatusUpdate{From: current, To: domain.StatusBlocked}, true, nil
	default:
		return domain.StatusUpdate{}, false, fmt.Errorf("worker: неожиданный тип события %q", eventType)
	}
}
