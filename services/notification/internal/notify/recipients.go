package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"bozor/pkg/shared/events"

	"bozor/services/notification/internal/domain"
)

// RecipientsConsumer — имя durable-консьюмера проекции получателей.
const RecipientsConsumer = "notification-recipients"

// RecipientStore — запись проекции получателя (реализуется repo.Repo).
type RecipientStore interface {
	UpsertRecipient(ctx context.Context, rec domain.Recipient) error
}

// Recipients проецирует bozor.user.created в локальную таблицу получателей:
// user_id → telegram_user_id + язык (единственный источник chat_id).
type Recipients struct {
	store RecipientStore
	log   *slog.Logger
}

// NewRecipients создаёт консьюмер проекции получателей.
func NewRecipients(store RecipientStore, log *slog.Logger) *Recipients {
	return &Recipients{store: store, log: log}
}

// userCreated — интересующая часть события bozor.user.created.
type userCreated struct {
	UserID         string `json:"user_id"`
	TelegramUserID int64  `json:"telegram_user_id"`
	LanguageCode   string `json:"language_code"`
}

// Handle сохраняет/обновляет проекцию получателя. Идемпотентно (upsert),
// поэтому inbox не нужен: повторная доставка безопасна.
func (c *Recipients) Handle(ctx context.Context, env events.Envelope) error {
	var p userCreated
	if err := env.Decode(&p); err != nil {
		return fmt.Errorf("notify: разбор bozor.user.created: %w", err)
	}
	if p.UserID == "" || p.TelegramUserID == 0 {
		return errors.New("notify: bozor.user.created без user_id/telegram_user_id")
	}

	rec := domain.Recipient{
		UserID:         p.UserID,
		TelegramUserID: p.TelegramUserID,
		LanguageCode:   domain.NormalizeLang(p.LanguageCode),
	}
	if err := c.store.UpsertRecipient(ctx, rec); err != nil {
		return err
	}
	c.log.InfoContext(ctx, "получатель спроецирован", slog.String("user_id", p.UserID))
	return nil
}
