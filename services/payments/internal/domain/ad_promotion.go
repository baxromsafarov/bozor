package domain

import (
	"errors"
	"time"
)

// Статусы применённой услуги.
const (
	PromotionActive    = "active"
	PromotionExpired   = "expired"
	PromotionRefunded  = "refunded"
	PromotionSuspended = "suspended"
)

// Статус объявления, при котором разрешено продвижение.
const AdStatusActive = "active"

// Известные коды услуг (значения сидируются каталогом 8.1; здесь — те, что
// требуют особой обработки: TOP даёт флаг is_top, BUMP — разовое поднятие).
const (
	ServiceTop  = "TOP"
	ServiceBump = "BUMP"
)

// Ошибки применения услуг.
var (
	ErrAdNotFound      = errors.New("payments: объявление не найдено")
	ErrNotAdOwner      = errors.New("payments: объявление принадлежит другому пользователю")
	ErrAdNotPromotable = errors.New("payments: объявление нельзя продвигать в текущем статусе")
	ErrNoPrice         = errors.New("payments: цена услуги не задана для объявления")
	ErrEmptyPromotion  = errors.New("payments: не указана услуга или набор")
	ErrUnknownService  = errors.New("payments: неизвестная услуга")
	ErrUnknownBundle   = errors.New("payments: неизвестный набор")
	ErrInvalidDuration = errors.New("payments: недопустимая длительность услуги")
)

// AdView — проекция объявления из Listing (владелец, категория/регион для цены).
type AdView struct {
	ID         string
	UserID     string
	CategoryID string
	RegionID   int
	Title      string
	Status     string
}

// AdPromotion — применённая к объявлению платная услуга.
type AdPromotion struct {
	ID          string
	AdID        string
	UserID      string
	ServiceCode string
	BundleCode  *string
	Status      string
	AmountUZS   int64
	StartsAt    time.Time
	EndsAt      *time.Time
	Schedule    []int
	CreatedAt   time.Time
}

// PromotionItem — один элемент плана применения: услуга с её сроком (EndsAt) или
// расписанием авто-BUMP (Schedule).
type PromotionItem struct {
	ServiceCode string
	EndsAt      *time.Time
	Schedule    []int
}

// IsTopService сообщает, является ли услуга топовой (для флага is_top в поиске).
func IsTopService(code string) bool { return code == ServiceTop }

// DueBump — созревший день авто-поднятия: услуга (promotion) и смещение дня в её
// расписании, у которого наступил момент поднятия (starts_at + day*24ч ≤ now) и
// который ещё не был исполнен. Воркер Stage 8.5 поднимает объявление для каждого.
type DueBump struct {
	PromotionID string
	AdID        string
	DayOffset   int
}
