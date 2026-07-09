// Package domain содержит сущности и правила Notification-сервиса: статусы
// доставки, проекцию получателя, маппинг доменных событий на группы настроек
// уведомлений и рендеринг локализованных шаблонов (uz/ru).
package domain

import (
	"strings"
	"time"

	"bozor/pkg/shared/events"
)

// Канал доставки. В v1 единственный — Telegram (ARCHITECTURE §4.10).
const ChannelTelegram = "telegram"

// Статусы доставки уведомления.
const (
	StatusPending = "pending" // в процессе (ожидает/повторяется)
	StatusSent    = "sent"    // доставлено
	StatusFailed  = "failed"  // постоянная ошибка канала
	StatusSkipped = "skipped" // не отправлено (настройки/нет получателя/канал выключен)
)

// Причины skipped/failed (для аудита).
const (
	ReasonPrefsDisabled   = "prefs_disabled"   // тип уведомления выключен пользователем
	ReasonNoRecipient     = "no_recipient"     // нет проекции получателя (неизвестен chat_id)
	ReasonChannelDisabled = "channel_disabled" // канал отключён (нет токена бота)
	ReasonPermanent       = "permanent_error"  // Bot API вернул неустранимую ошибку
)

// Группы настроек уведомлений (совпадают с типами в User/Profile
// notification_prefs; Notification разворачивает группу в конкретные события).
const (
	GroupAdStatus    = "ad_status"    // объявление одобрено/отклонено/истекло
	GroupChatMessage = "chat_message" // новое сообщение в чате
	GroupSavedSearch = "saved_search" // совпадение сохранённого поиска
	GroupReview      = "review"       // новый отзыв
	GroupPromotion   = "promotion"    // промо/платежи
	GroupNone        = ""             // без проверки настроек (напр. бан — всегда уведомляем)
)

// subjectGroups сопоставляет NATS subject группе настроек уведомлений.
// Значение GroupNone означает «уведомлять всегда, минуя настройки».
var subjectGroups = map[string]string{
	events.SubjectAdApproved:         GroupAdStatus,
	events.SubjectAdRejected:         GroupAdStatus,
	events.SubjectAdBlocked:          GroupAdStatus,
	events.SubjectAdExpired:          GroupAdStatus,
	events.SubjectChatMessageSent:    GroupChatMessage,
	events.SubjectSavedSearchMatched: GroupSavedSearch,
	events.SubjectReviewCreated:      GroupReview,
	events.SubjectPromotionActivated: GroupPromotion,
	events.SubjectPaymentSucceeded:   GroupPromotion,
	events.SubjectPaymentFailed:      GroupPromotion,
	events.SubjectWalletRefunded:     GroupPromotion,
	events.SubjectUserBanned:         GroupNone, // бан уведомляется всегда
}

// Subjects возвращает список subjects, которые слушает Notification-сервис
// (детерминированный порядок для конфигурации консьюмера и тестов).
func Subjects() []string {
	return []string{
		events.SubjectAdApproved,
		events.SubjectAdRejected,
		events.SubjectAdBlocked,
		events.SubjectAdExpired,
		events.SubjectChatMessageSent,
		events.SubjectSavedSearchMatched,
		events.SubjectReviewCreated,
		events.SubjectPromotionActivated,
		events.SubjectPaymentSucceeded,
		events.SubjectPaymentFailed,
		events.SubjectWalletRefunded,
		events.SubjectUserBanned,
	}
}

// PrefGroup возвращает группу настроек уведомлений для subject и признак того,
// что subject известен сервису. Для «всегда уведомлять» группа = GroupNone.
func PrefGroup(subject string) (group string, known bool) {
	g, ok := subjectGroups[subject]
	return g, ok
}

// Recipient — проекция получателя (из bozor.user.created).
type Recipient struct {
	UserID         string
	TelegramUserID int64
	LanguageCode   string
}

// Notification — запись журнала уведомлений и её статус доставки.
type Notification struct {
	ID        string
	EventID   string
	UserID    string
	EventType string
	Channel   string
	Body      string
	Status    string
	Reason    string
	Attempts  int
	CreatedAt time.Time
	SentAt    *time.Time
}

// EventPayload — объединённая нагрузка доменных событий, из которой строится
// уведомление. Каждое событие заполняет свой поднабор полей; отсутствующие
// поля остаются нулевыми и в шаблон не попадают.
type EventPayload struct {
	UserID        string `json:"user_id"` // получатель уведомления
	AdID          string `json:"ad_id"`
	Title         string `json:"title"`           // заголовок объявления
	Name          string `json:"name"`            // имя сохранённого поиска
	SavedSearchID string `json:"saved_search_id"` //nolint:tagliatelle
	Reason        string `json:"reason"`          // причина отклонения/бана
	Amount        int64  `json:"amount"`          // сумма платежа/возврата
	Currency      string `json:"currency"`
	SenderName    string `json:"sender_name"` // отправитель сообщения в чате
	Until         string `json:"until"`       // срок временного бана (текст, пусто = постоянный)
}

// NormalizeLang нормализует код языка к поддерживаемым "uz"/"ru"
// (по умолчанию — "ru").
func NormalizeLang(code string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(code)), "uz") {
		return "uz"
	}
	return "ru"
}
