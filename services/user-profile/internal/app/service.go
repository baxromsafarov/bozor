// Package app содержит use-cases User/Profile-сервиса: чтение/правку профиля,
// публичный профиль продавца (с кешем рейтинга) и настройки уведомлений.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/user-profile/internal/domain"
)

const source = "user-profile"

// Store — доступ к БД User/Profile (реализуется repo.Repo).
type Store interface {
	EnsureProfile(ctx context.Context, p domain.Profile) error
	GetProfile(ctx context.Context, userID string) (domain.Profile, error)
	UpdateProfileWithEvent(ctx context.Context, p domain.Profile, ev events.Envelope) error
	GetRating(ctx context.Context, userID string) (domain.Rating, error)
	GetNotificationPrefs(ctx context.Context, userID string) ([]domain.NotificationPref, error)
	ReplaceNotificationPrefs(ctx context.Context, userID string, prefs []domain.NotificationPref) error
}

// Service — use-cases профиля.
type Service struct {
	store Store
	log   *slog.Logger
}

// NewService создаёт сервис профиля.
func NewService(store Store, log *slog.Logger) *Service {
	return &Service{store: store, log: log}
}

// UpdateInput — частичное изменение профиля (nil-поле не меняется). Строковые
// поля-указатели на пустую строку очищают nullable-значение (аватар/город).
type UpdateInput struct {
	DisplayName         *string
	AvatarMediaID       *string
	About               *string
	UserType            *string
	BusinessName        *string
	CityID              *int64
	ContactPhoneVisible *bool
}

// PublicProfile — публичный профиль продавца (без контактов и настроек).
type PublicProfile struct {
	Profile domain.Profile
	Rating  domain.Rating
}

// Me возвращает профиль текущего пользователя, лениво создавая его по умолчанию,
// если событие bozor.user.created ещё не обработано (устойчивость к лагу шины).
func (s *Service) Me(ctx context.Context, userID string) (domain.Profile, error) {
	if err := s.store.EnsureProfile(ctx, domain.NewDefaultProfile(userID, "", time.Now().UTC())); err != nil {
		return domain.Profile{}, err
	}
	return s.store.GetProfile(ctx, userID)
}

// UpdateMe применяет частичное изменение профиля владельца и публикует
// bozor.user.updated. Профиль при необходимости создаётся лениво.
func (s *Service) UpdateMe(ctx context.Context, userID string, in UpdateInput) (domain.Profile, error) {
	current, err := s.Me(ctx, userID)
	if err != nil {
		return domain.Profile{}, err
	}
	updated, err := applyUpdate(current, in)
	if err != nil {
		return domain.Profile{}, err
	}
	if err := updated.Validate(); err != nil {
		return domain.Profile{}, err
	}

	ev, err := events.New(events.SubjectUserUpdated, source, newUserUpdated(updated))
	if err != nil {
		return domain.Profile{}, fmt.Errorf("app: сборка события: %w", err)
	}
	if err := s.store.UpdateProfileWithEvent(ctx, updated, ev); err != nil {
		return domain.Profile{}, err
	}
	return updated, nil
}

// PublicProfile возвращает публичный профиль продавца и его кеш рейтинга
// (ErrProfileNotFound, если профиля нет — без ленивого создания).
func (s *Service) PublicProfile(ctx context.Context, userID string) (PublicProfile, error) {
	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return PublicProfile{}, err
	}
	rating, err := s.store.GetRating(ctx, userID)
	if err != nil {
		return PublicProfile{}, err
	}
	return PublicProfile{Profile: p, Rating: rating}, nil
}

// NotificationPrefs возвращает эффективные настройки уведомлений (дефолты,
// переопределённые сохранёнными).
func (s *Service) NotificationPrefs(ctx context.Context, userID string) ([]domain.NotificationPref, error) {
	stored, err := s.store.GetNotificationPrefs(ctx, userID)
	if err != nil {
		return nil, err
	}
	return domain.EffectiveNotificationPrefs(stored), nil
}

// SetNotificationPrefs валидирует и заменяет набор настроек владельца, затем
// возвращает эффективный набор. Профиль при необходимости создаётся лениво
// (FK notification_prefs → profiles).
func (s *Service) SetNotificationPrefs(ctx context.Context, userID string, prefs []domain.NotificationPref) ([]domain.NotificationPref, error) {
	for _, p := range prefs {
		if !domain.ValidNotificationPref(p.Channel, p.EventType) {
			return nil, domain.ErrInvalidNotificationPref
		}
	}
	if err := s.store.EnsureProfile(ctx, domain.NewDefaultProfile(userID, "", time.Now().UTC())); err != nil {
		return nil, err
	}
	if err := s.store.ReplaceNotificationPrefs(ctx, userID, dedupePrefs(prefs)); err != nil {
		return nil, err
	}
	return s.NotificationPrefs(ctx, userID)
}

// applyUpdate накладывает частичное изменение на копию профиля.
func applyUpdate(p domain.Profile, in UpdateInput) (domain.Profile, error) {
	if in.DisplayName != nil {
		p.DisplayName = *in.DisplayName
	}
	if in.About != nil {
		p.About = *in.About
	}
	if in.UserType != nil {
		p.UserType = domain.UserType(*in.UserType)
	}
	if in.BusinessName != nil {
		p.BusinessName = *in.BusinessName
	}
	if in.ContactPhoneVisible != nil {
		p.ContactPhoneVisible = *in.ContactPhoneVisible
	}
	if in.CityID != nil {
		p.CityID = normalizeCityID(*in.CityID)
	}
	if in.AvatarMediaID != nil {
		avatar, err := normalizeAvatar(*in.AvatarMediaID)
		if err != nil {
			return domain.Profile{}, err
		}
		p.AvatarMediaID = avatar
	}
	return p, nil
}

// normalizeCityID: неположительный id очищает город (NULL).
func normalizeCityID(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}

// normalizeAvatar: пустая строка очищает аватар; иначе — валидный UUID медиа.
func normalizeAvatar(v string) (*string, error) {
	if v == "" {
		return nil, nil
	}
	if _, err := uuid.Parse(v); err != nil {
		return nil, domain.ErrInvalidAvatar
	}
	return &v, nil
}

// dedupePrefs оставляет последнюю запись на пару (канал, тип) — детерминированный
// набор для замены.
func dedupePrefs(prefs []domain.NotificationPref) []domain.NotificationPref {
	seen := make(map[string]int, len(prefs))
	out := make([]domain.NotificationPref, 0, len(prefs))
	for _, p := range prefs {
		key := p.Channel + "|" + p.EventType
		if idx, ok := seen[key]; ok {
			out[idx] = p
			continue
		}
		seen[key] = len(out)
		out = append(out, p)
	}
	return out
}

// userUpdated — payload события bozor.user.updated (без PII).
type userUpdated struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	UserType    string `json:"user_type"`
	CityID      *int64 `json:"city_id,omitempty"`
}

func newUserUpdated(p domain.Profile) userUpdated {
	return userUpdated{
		UserID:      p.UserID,
		DisplayName: p.DisplayName,
		UserType:    string(p.UserType),
		CityID:      p.CityID,
	}
}
