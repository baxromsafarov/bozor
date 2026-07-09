package domain

import (
	"errors"
	"time"
)

// Типы бана. temporary — со сроком; permanent — бессрочный; shadow — теневой
// (пользователь не видит блокировки, его контент скрыт — enforcement на будущее).
const (
	BanTemporary = "temporary"
	BanPermanent = "permanent"
	BanShadow    = "shadow"
)

var validBanTypes = map[string]bool{BanTemporary: true, BanPermanent: true, BanShadow: true}

// Ошибки валидации бана.
var (
	ErrInvalidBanType      = errors.New("недопустимый тип бана")
	ErrBanDurationRequired = errors.New("для временного бана требуется срок")
)

// Ban — бан пользователя.
type Ban struct {
	ID        string
	UserID    string
	Type      string
	Reason    string
	ExpiresAt *time.Time // срок для temporary; nil для permanent/shadow
	CreatedBy string
	CreatedAt time.Time
}

// ValidateBanInput проверяет тип бана и срок (temporary требует положительный срок).
func ValidateBanInput(banType string, duration time.Duration) error {
	if !validBanTypes[banType] {
		return ErrInvalidBanType
	}
	if banType == BanTemporary && duration <= 0 {
		return ErrBanDurationRequired
	}
	return nil
}

// BanExpiry вычисляет срок окончания бана: для temporary — now+duration, иначе nil.
func BanExpiry(banType string, now time.Time, duration time.Duration) *time.Time {
	if banType == BanTemporary {
		exp := now.Add(duration)
		return &exp
	}
	return nil
}
