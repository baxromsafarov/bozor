// Package domain содержит доменную модель чата: диалоги и сообщения,
// правила участия и валидацию тела сообщения.
package domain

import (
	"errors"
	"strings"
	"time"
)

// BodyMaxLen — максимум символов (рун) в одном сообщении.
const BodyMaxLen = 4000

// BlockedBody — текст, подставляемый вместо снятого модератором сообщения.
const BlockedBody = "[сообщение удалено модератором]"

// Доменные ошибки чата.
var (
	ErrSelfConversation     = errors.New("нельзя начать диалог с самим собой")
	ErrEmptyBody            = errors.New("пустое сообщение")
	ErrBodyTooLong          = errors.New("сообщение слишком длинное")
	ErrNotParticipant       = errors.New("пользователь не участник диалога")
	ErrConversationNotFound = errors.New("диалог не найден")
	ErrAdNotFound           = errors.New("объявление не найдено")
	ErrRateLimited          = errors.New("слишком часто — превышен лимит отправки")
	ErrUserBanned           = errors.New("пользователь заблокирован")
	ErrMessageNotFound      = errors.New("сообщение не найдено")
)

// Conversation — диалог покупатель↔продавец по объявлению.
type Conversation struct {
	ID            string
	AdID          string
	BuyerID       string // инициатор диалога
	SellerID      string // владелец объявления
	CreatedAt     time.Time
	LastMessageAt time.Time
	UnreadCount   int // непрочитанные для запросившего (заполняется в списке диалогов)
}

// ReadReceipt — отметка о прочтении: reader прочитал сообщения диалога до момента At.
// Уведомляет отправителя (второго участника), что его сообщения прочитаны.
type ReadReceipt struct {
	ConversationID string
	ReaderID       string
	At             time.Time
}

// Message — сообщение диалога.
type Message struct {
	ID             string
	ConversationID string
	SenderID       string
	Body           string
	CreatedAt      time.Time
	ReadAt         *time.Time
	Blocked        bool // снято модератором — тело скрыто (см. BlockedBody)
}

// Participant сообщает, участвует ли пользователь в диалоге.
func (c Conversation) Participant(userID string) bool {
	return userID == c.BuyerID || userID == c.SellerID
}

// Counterpart возвращает второго участника диалога — адресата сообщения senderID.
func (c Conversation) Counterpart(senderID string) string {
	if senderID == c.BuyerID {
		return c.SellerID
	}
	return c.BuyerID
}

// ValidateBody проверяет тело сообщения: непустое (после обрезки) и в пределах лимита рун.
func ValidateBody(body string) error {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ErrEmptyBody
	}
	if len([]rune(trimmed)) > BodyMaxLen {
		return ErrBodyTooLong
	}
	return nil
}

// AdView — проекция объявления из Listing (продавец — владелец объявления).
type AdView struct {
	ID     string
	UserID string
	Title  string
	Status string
}
