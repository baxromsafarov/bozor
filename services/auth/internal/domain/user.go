// Package domain содержит доменные сущности и правила Auth-сервиса.
package domain

import (
	"errors"
	"regexp"
	"strings"
)

// Доменные ошибки Auth.
var (
	// ErrContactNotOwned — контакт не принадлежит отправителю (переслан чужой).
	ErrContactNotOwned = errors.New("контакт не принадлежит отправителю")
	// ErrInvalidPhone — телефон не приводится к валидному узбекскому номеру.
	ErrInvalidPhone = errors.New("некорректный номер телефона")
)

// User — идентичность пользователя (расширенный профиль — в User/Profile).
type User struct {
	ID             string
	TelegramUserID int64
	Phone          string
	Username       string
	FirstName      string
	LastName       string
	LanguageCode   string
}

// nonDigit удаляет всё, кроме цифр.
var nonDigit = regexp.MustCompile(`\D`)

// NormalizePhoneUZ приводит телефон к E.164 для Узбекистана (+998XXXXXXXXX).
// Принимает 12 цифр с префиксом 998 либо 9 цифр (национальный номер);
// иначе возвращает ErrInvalidPhone.
func NormalizePhoneUZ(raw string) (string, error) {
	digits := nonDigit.ReplaceAllString(raw, "")
	switch {
	case len(digits) == 12 && strings.HasPrefix(digits, "998"):
		// уже в международном формате
	case len(digits) == 9:
		digits = "998" + digits
	default:
		return "", ErrInvalidPhone
	}
	return "+" + digits, nil
}

// NormalizeLang нормализует код языка к поддерживаемым "uz"/"ru"
// (по умолчанию — "ru").
func NormalizeLang(code string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(code)), "uz") {
		return "uz"
	}
	return "ru"
}
