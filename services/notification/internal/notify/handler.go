// Package notify содержит консьюмеры Notification-сервиса: доставку уведомлений
// по доменным событиям и проекцию получателей из bozor.user.created.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/notification/internal/domain"
	"bozor/services/notification/internal/telegram"
)

// DeliveryConsumer — имя durable-консьюмера доставки уведомлений.
const DeliveryConsumer = "notification-delivery"

// Store — операции доставки над БД (реализуется repo.Repo).
type Store interface {
	GetRecipient(ctx context.Context, userID string) (domain.Recipient, bool, error)
	RecordSkipped(ctx context.Context, id, eventID, userID, eventType string, payload []byte, reason string) error
	BeginDelivery(ctx context.Context, id, eventID, userID, eventType string, payload []byte, body string) (bool, error)
	MarkSent(ctx context.Context, eventID string) error
	MarkFailed(ctx context.Context, eventID, reason string) error
	MarkSkipped(ctx context.Context, eventID, reason string) error
}

// Prefs — проверка настроек уведомлений получателя (реализуется prefs.Client).
type Prefs interface {
	Enabled(ctx context.Context, userID, group string) (bool, error)
}

// Sender — канал доставки (реализуется telegram.Client).
type Sender interface {
	Send(ctx context.Context, chatID int64, text string) error
}

// Waiter — ограничитель частоты отправок (реализуется *rate.Limiter).
type Waiter interface {
	Wait(ctx context.Context) error
}

// Handler доставляет уведомления по доменным событиям.
type Handler struct {
	store          Store
	prefs          Prefs
	sender         Sender
	limiter        Waiter
	channelEnabled bool
	newID          func() (string, error)
	log            *slog.Logger
}

// NewHandler создаёт обработчик доставки. channelEnabled=false (нет токена бота)
// переводит все уведомления в статус skipped(channel_disabled).
func NewHandler(store Store, prefs Prefs, sender Sender, limiter Waiter, channelEnabled bool, log *slog.Logger) *Handler {
	return &Handler{
		store:          store,
		prefs:          prefs,
		sender:         sender,
		limiter:        limiter,
		channelEnabled: channelEnabled,
		newID:          func() (string, error) { id, err := uuid.NewV7(); return id.String(), err },
		log:            log,
	}
}

// Handle обрабатывает одно доменное событие: определяет получателя, учитывает
// настройки уведомлений, рендерит локализованный шаблон и отправляет через канал.
// Идемпотентно по env.ID (одно событие → не более одной доставки).
func (h *Handler) Handle(ctx context.Context, env events.Envelope) error {
	group, known := domain.PrefGroup(env.Type)
	if !known {
		h.log.WarnContext(ctx, "неизвестный тип события — пропуск", slog.String("subject", env.Type))
		return nil // consume-фильтр не должен такое пропускать; защита от мусора
	}

	var p domain.EventPayload
	if err := env.Decode(&p); err != nil {
		return fmt.Errorf("notify: разбор события %s: %w", env.Type, err)
	}
	if p.UserID == "" {
		h.log.WarnContext(ctx, "в событии нет получателя — пропуск",
			slog.String("subject", env.Type), slog.String("event_id", env.ID))
		return nil
	}

	id, err := h.newID()
	if err != nil {
		return fmt.Errorf("notify: генерация id: %w", err)
	}

	// Канал отключён (нет токена бота): фиксируем skipped и подтверждаем.
	if !h.channelEnabled {
		return h.skip(ctx, id, env, p.UserID, domain.ReasonChannelDisabled)
	}

	rec, found, err := h.store.GetRecipient(ctx, p.UserID)
	if err != nil {
		return err
	}
	if !found {
		// Проекция получателя ещё не готова (лаг bozor.user.created) либо
		// пользователь неизвестен: повторяем с backoff, после исчерпания — DLQ.
		return fmt.Errorf("notify: получатель %s неизвестен (нет проекции)", p.UserID)
	}

	enabled, err := h.prefs.Enabled(ctx, p.UserID, group)
	if err != nil {
		return err // недоступность User/Profile — повторяемо
	}
	if !enabled {
		return h.skip(ctx, id, env, p.UserID, domain.ReasonPrefsDisabled)
	}

	body, ok := domain.Render(env.Type, rec.LanguageCode, p)
	if !ok {
		h.log.WarnContext(ctx, "нет шаблона для события — пропуск", slog.String("subject", env.Type))
		return h.skip(ctx, id, env, p.UserID, domain.ReasonPermanent)
	}

	proceed, err := h.store.BeginDelivery(ctx, id, env.ID, p.UserID, env.Type, env.Data, body)
	if err != nil {
		return err
	}
	if !proceed {
		return nil // уже доставлено/в терминальном статусе — не дублируем
	}

	if err := h.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("notify: ожидание лимитера: %w", err)
	}

	return h.deliver(ctx, env.ID, rec.TelegramUserID, body)
}

// deliver отправляет сообщение и фиксирует исход в журнале.
func (h *Handler) deliver(ctx context.Context, eventID string, chatID int64, body string) error {
	err := h.sender.Send(ctx, chatID, body)
	switch {
	case err == nil:
		return h.store.MarkSent(ctx, eventID)

	case errors.Is(err, telegram.ErrDisabled):
		return h.store.MarkSkipped(ctx, eventID, domain.ReasonChannelDisabled)

	default:
		var se *telegram.SendError
		if errors.As(err, &se) && !se.Retryable {
			h.log.WarnContext(ctx, "постоянная ошибка доставки",
				slog.String("event_id", eventID), slog.String("error", se.Error()))
			return h.store.MarkFailed(ctx, eventID, domain.ReasonPermanent+": "+se.Description)
		}
		// Повторяемо (429/5xx/сеть): вернуть ошибку → Nak → повтор с backoff.
		h.log.WarnContext(ctx, "временная ошибка доставки, повтор",
			slog.String("event_id", eventID), slog.String("error", err.Error()))
		return err
	}
}

// skip фиксирует пропуск доставки (терминально, идемпотентно) и подтверждает событие.
func (h *Handler) skip(ctx context.Context, id string, env events.Envelope, userID, reason string) error {
	if err := h.store.RecordSkipped(ctx, id, env.ID, userID, env.Type, env.Data, reason); err != nil {
		return err
	}
	h.log.InfoContext(ctx, "уведомление пропущено",
		slog.String("subject", env.Type), slog.String("user_id", userID), slog.String("reason", reason))
	return nil
}
