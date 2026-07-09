// Package domain содержит доменные сущности и правила Listing-сервиса.
package domain

import (
	"errors"
	"slices"
	"time"
)

// Доменные ошибки объявлений.
var (
	ErrAdNotFound          = errors.New("объявление не найдено")
	ErrInvalidTransition   = errors.New("недопустимый переход статуса")
	ErrEmptyTitle          = errors.New("пустой заголовок")
	ErrTitleTooLong        = errors.New("заголовок слишком длинный")
	ErrNegativePrice       = errors.New("отрицательная цена")
	ErrMissingCategory     = errors.New("не указана категория")
	ErrMissingRegion       = errors.New("не указан регион")
	ErrTooManyImages       = errors.New("слишком много изображений")
	ErrMultipleCovers      = errors.New("допустима только одна обложка")
	ErrUnknownAttribute    = errors.New("неизвестный атрибут для категории")
	ErrMissingRequiredAttr = errors.New("не заполнен обязательный атрибут")
	ErrInvalidAttrValue    = errors.New("недопустимое значение атрибута")
)

// Ограничения объявления.
const (
	MaxTitleLen = 120
	MaxImages   = 10
)

// Status — статус жизненного цикла объявления.
type Status string

// Статусы объявления (статусная машина — ARCHITECTURE §4.5).
const (
	StatusDraft    Status = "draft"
	StatusPending  Status = "pending"
	StatusActive   Status = "active"
	StatusRejected Status = "rejected"
	StatusSold     Status = "sold"
	StatusExpired  Status = "expired"
	StatusArchived Status = "archived"
	StatusBlocked  Status = "blocked"
)

// transitions — разрешённые переходы (кроме перехода в blocked из любого статуса).
var transitions = map[Status][]Status{
	StatusDraft:    {StatusPending},
	StatusPending:  {StatusActive, StatusRejected},
	StatusRejected: {StatusPending},                                            // после правки — снова на модерацию
	StatusActive:   {StatusSold, StatusExpired, StatusArchived, StatusPending}, // pending — повторная модерация после правки
	StatusExpired:  {StatusActive},                                             // реактивация продлением (renew)
}

// CanTransitionTo сообщает, допустим ли переход из s в to. Переход в blocked
// разрешён из любого статуса (действие модерации).
func (s Status) CanTransitionTo(to Status) bool {
	if to == StatusBlocked {
		return true
	}
	return slices.Contains(transitions[s], to)
}

// Valid сообщает, является ли значение известным статусом.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusPending, StatusActive, StatusRejected,
		StatusSold, StatusExpired, StatusArchived, StatusBlocked:
		return true
	}
	return false
}

// StatusUpdate описывает переход статуса объявления и опциональные отметки
// времени. Nil-поля времени не изменяются в БД (COALESCE в репозитории), поэтому
// переходы жизненного цикла лишь проставляют published_at/expires_at, но не сбрасывают их.
type StatusUpdate struct {
	From        Status
	To          Status
	PublishedAt *time.Time
	ExpiresAt   *time.Time
}

// Ad — объявление (источник истины).
type Ad struct {
	ID           string
	UserID       string
	CategoryID   string
	Title        string
	Description  string
	Price        int64 // тийины
	Currency     string
	RegionID     int16
	CityID       *int64
	Lat          *float64
	Lng          *float64
	Status       Status
	PhoneDisplay bool
	PublishedAt  *time.Time
	ExpiresAt    *time.Time
	BumpedAt     *time.Time
	ViewsCount   int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Attributes   []AdAttributeValue
	Images       []AdImage
}

// AdAttributeValue — значение атрибута объявления (ключ — slug из Catalog).
type AdAttributeValue struct {
	AttributeSlug string
	Value         string
}

// AdImage — привязка изображения к объявлению.
type AdImage struct {
	MediaID   string
	SortOrder int
	IsCover   bool
}

// ValidateCore проверяет обязательные поля объявления (без учёта атрибутов).
func (a *Ad) ValidateCore() error {
	switch {
	case a.Title == "":
		return ErrEmptyTitle
	case len(a.Title) > MaxTitleLen:
		return ErrTitleTooLong
	case a.Price < 0:
		return ErrNegativePrice
	case a.CategoryID == "":
		return ErrMissingCategory
	case a.RegionID == 0:
		return ErrMissingRegion
	}
	return nil
}

// ValidateImages проверяет число изображений и единственность обложки.
func ValidateImages(images []AdImage) error {
	if len(images) > MaxImages {
		return ErrTooManyImages
	}
	covers := 0
	for _, img := range images {
		if img.IsCover {
			covers++
		}
	}
	if covers > 1 {
		return ErrMultipleCovers
	}
	return nil
}
