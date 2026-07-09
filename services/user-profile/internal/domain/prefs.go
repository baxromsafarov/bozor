package domain

import (
	"errors"
	"slices"
)

// Каналы доставки уведомлений. В v1 единственный канал — Telegram (Notification
// отправляет через Bot API, ARCHITECTURE §4.11); поле channel заложено на вырост.
const ChannelTelegram = "telegram"

// Типы уведомлений, доступные пользователю для включения/выключения. Сгруппированы
// по смыслу (не 1:1 с доменными событиями): Notification разворачивает их в
// конкретные события при рассылке.
const (
	NotifyAdStatus    = "ad_status"    // объявление одобрено/отклонено/истекло
	NotifyChatMessage = "chat_message" // новое сообщение в чате
	NotifySavedSearch = "saved_search" // совпадение сохранённого поиска
	NotifyReview      = "review"       // новый отзыв о вас
	NotifyPromotion   = "promotion"    // промо/платёж
)

// ErrInvalidNotificationPref — недопустимый канал или тип уведомления.
var ErrInvalidNotificationPref = errors.New("недопустимая настройка уведомления")

// notificationChannels — допустимые каналы.
var notificationChannels = map[string]bool{ChannelTelegram: true}

// notificationEventTypes — допустимые типы уведомлений (порядок — для дефолтов).
var notificationEventTypes = []string{
	NotifyAdStatus, NotifyChatMessage, NotifySavedSearch, NotifyReview, NotifyPromotion,
}

// NotificationPref — включённость уведомлений по каналу и типу события.
type NotificationPref struct {
	Channel   string
	EventType string
	Enabled   bool
}

// ValidNotificationPref сообщает, что канал и тип уведомления известны.
func ValidNotificationPref(channel, eventType string) bool {
	if !notificationChannels[channel] {
		return false
	}
	return slices.Contains(notificationEventTypes, eventType)
}

// DefaultNotificationPrefs — полный набор настроек по умолчанию (все включены).
func DefaultNotificationPrefs() []NotificationPref {
	out := make([]NotificationPref, 0, len(notificationEventTypes))
	for _, t := range notificationEventTypes {
		out = append(out, NotificationPref{Channel: ChannelTelegram, EventType: t, Enabled: true})
	}
	return out
}

// EffectiveNotificationPrefs накладывает сохранённые настройки на набор по
// умолчанию: возвращает полный список известных (канал, тип) с фактической
// включённостью (сохранённая строка переопределяет дефолт «включено»).
func EffectiveNotificationPrefs(stored []NotificationPref) []NotificationPref {
	override := make(map[string]bool, len(stored))
	for _, p := range stored {
		override[p.Channel+"|"+p.EventType] = p.Enabled
	}
	out := DefaultNotificationPrefs()
	for i := range out {
		if v, ok := override[out[i].Channel+"|"+out[i].EventType]; ok {
			out[i].Enabled = v
		}
	}
	return out
}
