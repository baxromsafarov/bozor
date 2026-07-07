// Package app содержит use-cases Auth-сервиса.
package app

import (
	"context"

	"github.com/google/uuid"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/events"

	"bozor/services/auth/internal/domain"
)

const source = "auth"

// Store — персистентность пользователей (реализуется repo.UserRepo).
type Store interface {
	UpsertUserWithEvent(ctx context.Context, u domain.User, ev events.Envelope) (created bool, err error)
}

// Service — use-cases Auth-сервиса.
type Service struct {
	store Store
}

// NewService создаёт use-case-сервис поверх хранилища.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Contact — данные из Telegram-сообщения с контактом.
type Contact struct {
	FromID        int64  // id отправителя (message.from.id)
	ContactUserID int64  // message.contact.user_id
	PhoneNumber   string // message.contact.phone_number
	FirstName     string
	LastName      string
	Username      string
	LanguageCode  string
}

// RegisterContact обрабатывает присланный контакт: проверяет владение,
// нормализует телефон, апсертит пользователя; при первом создании публикует
// bozor.user.created (через outbox). Возвращает признак «создан впервые».
func (s *Service) RegisterContact(ctx context.Context, c Contact) (bool, error) {
	// КРИТИЧНО (безопасность): контакт должен принадлежать отправителю —
	// иначе пользователь переслал чужой контакт.
	if c.ContactUserID == 0 || c.ContactUserID != c.FromID {
		return false, apperr.Wrap(domain.ErrContactNotOwned, apperr.KindForbidden, "contact_not_owned",
			"Отправьте свой номер кнопкой", "O'z raqamingizni tugma orqali yuboring")
	}

	phone, err := domain.NormalizePhoneUZ(c.PhoneNumber)
	if err != nil {
		return false, apperr.Wrap(err, apperr.KindInvalid, "invalid_phone",
			"Некорректный номер телефона", "Telefon raqami noto'g'ri")
	}

	id, err := uuid.NewV7()
	if err != nil {
		return false, apperr.Wrap(err, apperr.KindInternal, "id_gen",
			"Внутренняя ошибка", "Ichki xatolik")
	}

	lang := domain.NormalizeLang(c.LanguageCode)
	u := domain.User{
		ID:             id.String(),
		TelegramUserID: c.FromID,
		Phone:          phone,
		Username:       c.Username,
		FirstName:      c.FirstName,
		LastName:       c.LastName,
		LanguageCode:   lang,
	}

	// Телефон в событие не кладём (PII); достаточно идентификаторов и языка.
	ev, err := events.New(events.SubjectUserCreated, source, userCreated{
		UserID:         u.ID,
		TelegramUserID: u.TelegramUserID,
		LanguageCode:   lang,
	})
	if err != nil {
		return false, apperr.Wrap(err, apperr.KindInternal, "event_build",
			"Внутренняя ошибка", "Ichki xatolik")
	}

	return s.store.UpsertUserWithEvent(ctx, u, ev)
}

// userCreated — payload события bozor.user.created.
type userCreated struct {
	UserID         string `json:"user_id"`
	TelegramUserID int64  `json:"telegram_user_id"`
	LanguageCode   string `json:"language_code"`
}
