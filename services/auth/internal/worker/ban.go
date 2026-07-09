// Package worker содержит фоновые консьюмеры Auth-сервиса.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"bozor/pkg/shared/events"
)

// BanConsumer — имя durable-консьюмера обработки банов.
const BanConsumer = "auth-ban"

// BanStore — операции бана над БД (реализуется repo.UserRepo).
type BanStore interface {
	BanUser(ctx context.Context, userID string) (int64, error)
}

// Banner реагирует на bozor.user.banned: помечает пользователя забаненным и
// отзывает его сессии (refresh-токены) — банированный теряет доступ.
type Banner struct {
	store BanStore
	log   *slog.Logger
}

// NewBanner создаёт консьюмер банов.
func NewBanner(store BanStore, log *slog.Logger) *Banner {
	return &Banner{store: store, log: log}
}

// userBanned — интересующая часть события bozor.user.banned.
type userBanned struct {
	UserID string `json:"user_id"`
}

// Handle обрабатывает бан: отзывает сессии пользователя. Идемпотентно
// (BanUser не зависит от повторов), поэтому inbox не нужен.
func (b *Banner) Handle(ctx context.Context, env events.Envelope) error {
	var pl userBanned
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор bozor.user.banned: %w", err)
	}
	if pl.UserID == "" {
		return errors.New("worker: пустой user_id в bozor.user.banned")
	}
	revoked, err := b.store.BanUser(ctx, pl.UserID)
	if err != nil {
		return err
	}
	b.log.InfoContext(ctx, "пользователь забанен, сессии отозваны",
		slog.String("user_id", pl.UserID), slog.Int64("revoked_tokens", revoked))
	return nil
}
