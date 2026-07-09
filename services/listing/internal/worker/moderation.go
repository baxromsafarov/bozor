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
// nil — к подтверждению. Идемпотентно: inbox + переход только из статуса pending —
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
	if ad.Status != domain.StatusPending {
		// Не в модерации (уже решено/снято) — работа не нужна, но событие отметим.
		return m.store.MarkEventProcessed(ctx, ModerationConsumer, env.ID)
	}

	upd, err := m.plan(env.Type)
	if err != nil {
		return err
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

// plan строит переход из типа события: approved → active (с published_at и
// expires_at = now + adTTL), rejected → rejected.
func (m *Moderator) plan(eventType string) (domain.StatusUpdate, error) {
	upd := domain.StatusUpdate{From: domain.StatusPending}
	switch eventType {
	case events.SubjectAdApproved:
		now := time.Now().UTC()
		exp := now.Add(m.adTTL)
		upd.To, upd.PublishedAt, upd.ExpiresAt = domain.StatusActive, &now, &exp
	case events.SubjectAdRejected:
		upd.To = domain.StatusRejected
	default:
		return domain.StatusUpdate{}, fmt.Errorf("worker: неожиданный тип события %q", eventType)
	}
	return upd, nil
}
