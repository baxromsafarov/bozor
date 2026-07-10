// Package domain содержит доменные сущности и правила Reviews-сервиса.
package domain

import (
	"errors"
	"strings"
	"time"
)

// Статусы отзыва.
const (
	StatusActive  = "active"  // виден в ленте и учитывается в рейтинге
	StatusBlocked = "blocked" // снят модератором по жалобе
)

// Ограничения отзыва.
const (
	RatingMin  = 1
	RatingMax  = 5
	MaxBodyLen = 2000 // рун
)

// Доменные ошибки отзывов.
var (
	ErrInvalidRating   = errors.New("оценка вне диапазона 1..5")
	ErrBodyTooLong     = errors.New("текст отзыва слишком длинный")
	ErrSelfReview      = errors.New("нельзя оставить отзыв о своём объявлении")
	ErrAdNotFound      = errors.New("объявление не найдено")
	ErrDuplicateReview = errors.New("отзыв по этому объявлению уже оставлен")
	ErrReviewNotFound  = errors.New("отзыв не найден")
)

// Review — отзыв покупателя о продавце в контексте объявления.
type Review struct {
	ID        string
	AdID      string
	AuthorID  string
	TargetID  string
	Rating    int
	Body      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AdView — проекция объявления из Listing: владелец (продавец) для адресата отзыва.
type AdView struct {
	ID     string
	UserID string
	Status string
}

// ValidateRating проверяет оценку (целое 1..5).
func ValidateRating(rating int) error {
	if rating < RatingMin || rating > RatingMax {
		return ErrInvalidRating
	}
	return nil
}

// NormalizeBody обрезает пробелы по краям и проверяет длину (в рунах). Текст
// необязателен (пустой допустим — отзыв только с оценкой).
func NormalizeBody(body string) (string, error) {
	b := strings.TrimSpace(body)
	if len([]rune(b)) > MaxBodyLen {
		return "", ErrBodyTooLong
	}
	return b, nil
}
