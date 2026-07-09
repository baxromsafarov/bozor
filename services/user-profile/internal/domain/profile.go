// Package domain содержит доменную модель User/Profile-сервиса: профиль
// пользователя, тип аккаунта, настройки уведомлений и кеш рейтинга.
package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// UserType — тип аккаунта пользователя. business заложен на вырост (витрины —
// вне scope v1, ARCHITECTURE §4.2).
type UserType string

const (
	UserTypeIndividual UserType = "individual"
	UserTypeBusiness   UserType = "business"
)

// Valid сообщает, что тип аккаунта допустим.
func (t UserType) Valid() bool {
	return t == UserTypeIndividual || t == UserTypeBusiness
}

// Пределы полей профиля (в рунах — пользовательский текст произвольного языка).
const (
	MaxDisplayName  = 100
	MaxAbout        = 2000
	MaxBusinessName = 120
)

// defaultLanguage — язык по умолчанию при отсутствии в событии.
const defaultLanguage = "ru"

// Ошибки домена профиля.
var (
	ErrProfileNotFound      = errors.New("профиль не найден")
	ErrInvalidUserType      = errors.New("недопустимый тип аккаунта")
	ErrDisplayNameTooLong   = errors.New("слишком длинное отображаемое имя")
	ErrAboutTooLong         = errors.New("слишком длинное описание")
	ErrBusinessNameTooLong  = errors.New("слишком длинное название бизнеса")
	ErrBusinessNameRequired = errors.New("для бизнес-аккаунта требуется название")
	ErrInvalidAvatar        = errors.New("недопустимый идентификатор аватара")
)

// Profile — расширенный профиль пользователя.
type Profile struct {
	UserID              string
	DisplayName         string
	AvatarMediaID       *string
	About               string
	UserType            UserType
	BusinessName        string
	CityID              *int64
	ContactPhoneVisible bool
	LanguageCode        string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// NewDefaultProfile строит профиль по умолчанию для нового пользователя
// (создаётся по bozor.user.created или лениво при первом обращении к /me).
func NewDefaultProfile(userID, languageCode string, now time.Time) Profile {
	if languageCode == "" {
		languageCode = defaultLanguage
	}
	return Profile{
		UserID:              userID,
		UserType:            UserTypeIndividual,
		ContactPhoneVisible: true,
		LanguageCode:        languageCode,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

// Validate проверяет инварианты профиля.
func (p Profile) Validate() error {
	if !p.UserType.Valid() {
		return ErrInvalidUserType
	}
	if utf8.RuneCountInString(p.DisplayName) > MaxDisplayName {
		return ErrDisplayNameTooLong
	}
	if utf8.RuneCountInString(p.About) > MaxAbout {
		return ErrAboutTooLong
	}
	if utf8.RuneCountInString(p.BusinessName) > MaxBusinessName {
		return ErrBusinessNameTooLong
	}
	if p.UserType == UserTypeBusiness && strings.TrimSpace(p.BusinessName) == "" {
		return ErrBusinessNameRequired
	}
	return nil
}

// Rating — кеш агрегированного рейтинга продавца (обновляется из Reviews).
type Rating struct {
	AvgRating    float64
	ReviewsCount int
}
