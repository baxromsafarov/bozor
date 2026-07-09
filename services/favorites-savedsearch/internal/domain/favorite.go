// Package domain содержит доменную модель Favorites/SavedSearch-сервиса.
package domain

import (
	"errors"
	"time"
)

// ErrInvalidAdID — некорректный идентификатор объявления (не UUID).
var ErrInvalidAdID = errors.New("некорректный идентификатор объявления")

// Favorite — запись избранного: пара пользователь/объявление.
type Favorite struct {
	UserID    string
	AdID      string
	CreatedAt time.Time
}
