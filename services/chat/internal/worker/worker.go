// Package worker содержит фоновые консьюмеры Chat-сервиса: проекция банов и
// снятие сообщений модератором.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"bozor/pkg/shared/events"
)

// Имена durable-консьюмеров.
const (
	BanConsumer   = "chat-ban"
	BlockConsumer = "chat-message-block"
)

// Store — операции проекции банов и снятия сообщений (реализуется repo.Repo).
type Store interface {
	BanUser(ctx context.Context, userID, reason string) error
	BlockMessage(ctx context.Context, messageID string) error
	IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, consumer, eventID string) error
}

// Moderator реагирует на баны и снятия сообщений: забаненный не может писать,
// снятое сообщение скрывается.
type Moderator struct {
	store Store
	log   *slog.Logger
}

// New создаёт консьюмер модерации чата.
func New(store Store, log *slog.Logger) *Moderator {
	return &Moderator{store: store, log: log}
}

type userBanned struct {
	UserID string `json:"user_id"`
	Reason string `json:"reason"`
}

type messageBlocked struct {
	MessageID string `json:"message_id"`
	Reason    string `json:"reason"`
}

// HandleBan проецирует bozor.user.banned: забаненный не пишет и не начинает
// диалоги. Идемпотентно через inbox.
func (m *Moderator) HandleBan(ctx context.Context, env events.Envelope) error {
	var pl userBanned
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор bozor.user.banned: %w", err)
	}
	if pl.UserID == "" {
		return errors.New("worker: пустой user_id в bozor.user.banned")
	}
	return m.once(ctx, BanConsumer, env.ID, func() error {
		return m.store.BanUser(ctx, pl.UserID, pl.Reason)
	}, "пользователь заблокирован в чате", slog.String("user_id", pl.UserID))
}

// HandleBlock снимает сообщение по bozor.chat.message_blocked. Идемпотентно через inbox.
func (m *Moderator) HandleBlock(ctx context.Context, env events.Envelope) error {
	var pl messageBlocked
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор bozor.chat.message_blocked: %w", err)
	}
	if pl.MessageID == "" {
		return errors.New("worker: пустой message_id в bozor.chat.message_blocked")
	}
	return m.once(ctx, BlockConsumer, env.ID, func() error {
		return m.store.BlockMessage(ctx, pl.MessageID)
	}, "сообщение снято модератором", slog.String("message_id", pl.MessageID))
}

// once выполняет работу один раз на событие (inbox) и логирует результат.
func (m *Moderator) once(ctx context.Context, consumer, eventID string, do func() error, msg string, attrs ...any) error {
	processed, err := m.store.IsEventProcessed(ctx, consumer, eventID)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}
	if err := do(); err != nil {
		return err
	}
	m.log.InfoContext(ctx, msg, attrs...)
	return m.store.MarkEventProcessed(ctx, consumer, eventID)
}
