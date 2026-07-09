// Package worker содержит фоновые процессы User/Profile-сервиса: потребление
// bozor.user.created (создание профиля по умолчанию, идемпотентно через inbox).
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/user-profile/internal/domain"
)

// UsersConsumer — имя durable-консьюмера и ключ inbox-идемпотентности.
const UsersConsumer = "user-profile-users"

// UsersStore — операции БД, нужные обработчику bozor.user.created.
type UsersStore interface {
	IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error)
	CreateProfileWithInbox(ctx context.Context, p domain.Profile, consumer, eventID string) error
}

// Users потребляет bozor.user.created и создаёт профиль по умолчанию.
type Users struct {
	store UsersStore
	log   *slog.Logger
}

// NewUsers создаёт обработчик события создания пользователя.
func NewUsers(store UsersStore, log *slog.Logger) *Users {
	return &Users{store: store, log: log}
}

// userCreated — интересующая часть события bozor.user.created.
type userCreated struct {
	UserID       string `json:"user_id"`
	LanguageCode string `json:"language_code"`
}

// Handle обрабатывает одно событие создания пользователя. Ошибка → повтор/DLQ
// (natsx), nil → подтверждение. Идемпотентно: inbox + вставка ON CONFLICT DO NOTHING.
func (u *Users) Handle(ctx context.Context, env events.Envelope) error {
	var pl userCreated
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("worker: разбор bozor.user.created: %w", err)
	}
	if pl.UserID == "" {
		return errors.New("worker: пустой user_id в bozor.user.created")
	}

	processed, err := u.store.IsEventProcessed(ctx, UsersConsumer, env.ID)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}

	p := domain.NewDefaultProfile(pl.UserID, pl.LanguageCode, time.Now().UTC())
	u.log.InfoContext(ctx, "создание профиля по событию", slog.String("user_id", pl.UserID))
	return u.store.CreateProfileWithInbox(ctx, p, UsersConsumer, env.ID)
}
