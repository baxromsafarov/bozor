package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Пределы сохранённых поисков.
const (
	MaxSavedSearchName = 100
	MaxSavedSearches   = 50 // на пользователя
)

// Ошибки домена сохранённых поисков.
var (
	ErrSavedSearchNotFound  = errors.New("сохранённый поиск не найден")
	ErrTooManySavedSearches = errors.New("превышен лимит сохранённых поисков")
	ErrEmptyName            = errors.New("пустое имя сохранённого поиска")
	ErrNameTooLong          = errors.New("слишком длинное имя сохранённого поиска")
)

// SearchQuery — условия сохранённого поиска (зеркалит фильтры Search 4.3).
type SearchQuery struct {
	Text       string            `json:"text,omitempty"`
	CategoryID string            `json:"category_id,omitempty"`
	RegionID   int16             `json:"region_id,omitempty"`
	CityID     int64             `json:"city_id,omitempty"`
	PriceMin   *int64            `json:"price_min,omitempty"`
	PriceMax   *int64            `json:"price_max,omitempty"`
	Currency   string            `json:"currency,omitempty"`
	Attrs      map[string]string `json:"attrs,omitempty"`
}

// SavedSearch — сохранённый поиск пользователя с алертами о совпадениях.
type SavedSearch struct {
	ID             string
	UserID         string
	Name           string
	Query          SearchQuery
	NotifyEnabled  bool
	LastNotifiedAt *time.Time
	CreatedAt      time.Time
}

// AdView — проекция объявления для оценки фильтров (читается из Listing).
type AdView struct {
	ID          string
	CategoryID  string
	RegionID    int16
	CityID      *int64
	Price       int64
	Currency    string
	Title       string
	Description string
	Attrs       map[string]string
}

// Matches сообщает, удовлетворяет ли объявление условиям сохранённого поиска.
// Структурные фильтры (категория/регион/город/цена/валюта/атрибуты) — точные;
// текст — приблизительное совпадение подстрокой (полнотекст/typo живут в Search).
func (q SearchQuery) Matches(ad AdView) bool {
	if q.CategoryID != "" && q.CategoryID != ad.CategoryID {
		return false
	}
	if q.RegionID != 0 && q.RegionID != ad.RegionID {
		return false
	}
	if q.CityID != 0 && (ad.CityID == nil || *ad.CityID != q.CityID) {
		return false
	}
	if q.Currency != "" && !strings.EqualFold(q.Currency, ad.Currency) {
		return false
	}
	if q.PriceMin != nil && ad.Price < *q.PriceMin {
		return false
	}
	if q.PriceMax != nil && ad.Price > *q.PriceMax {
		return false
	}
	for slug, val := range q.Attrs {
		if ad.Attrs[slug] != val {
			return false
		}
	}
	if q.Text != "" && !matchesText(q.Text, ad) {
		return false
	}
	return true
}

// matchesText — приблизительное совпадение текста (подстрока без учёта регистра)
// по заголовку/описанию — достаточно для алерта, точный поиск — в Search.
func matchesText(text string, ad AdView) bool {
	needle := strings.ToLower(strings.TrimSpace(text))
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(ad.Title), needle) ||
		strings.Contains(strings.ToLower(ad.Description), needle)
}

// ValidateName проверяет имя сохранённого поиска.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrEmptyName
	}
	if utf8.RuneCountInString(name) > MaxSavedSearchName {
		return ErrNameTooLong
	}
	return nil
}
