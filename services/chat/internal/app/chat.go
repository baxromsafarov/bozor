// Package app содержит use-cases Chat-сервиса: старт диалога по объявлению,
// отправка сообщения, чтение диалогов и истории.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/chat/internal/domain"
)

const source = "chat"

// Доменные ошибки, проксируемые транспорту (для маппинга в RFC 7807).
var (
	ErrConversationNotFound = domain.ErrConversationNotFound
	ErrNotParticipant       = domain.ErrNotParticipant
	ErrAdNotFound           = domain.ErrAdNotFound
	ErrSelfConversation     = domain.ErrSelfConversation
)

// Store — доступ к диалогам/сообщениям (реализуется repo.Repo).
type Store interface {
	EnsureConversation(ctx context.Context, conv domain.Conversation) (domain.Conversation, error)
	GetConversation(ctx context.Context, id string) (domain.Conversation, bool, error)
	ListConversations(ctx context.Context, userID string, limit int) ([]domain.Conversation, error)
	ListMessages(ctx context.Context, conversationID string, limit int) ([]domain.Message, error)
	InsertMessageWithEvent(ctx context.Context, msg domain.Message, ev events.Envelope) error
}

// AdSource — чтение объявления (продавец = владелец) из Listing.
type AdSource interface {
	GetAd(ctx context.Context, id string) (domain.AdView, bool, error)
}

// Service — use-cases чата.
type Service struct {
	store Store
	ads   AdSource
	now   func() time.Time
	newID func() (string, error)
	log   *slog.Logger
}

// NewService создаёт сервис чата.
func NewService(store Store, ads AdSource, log *slog.Logger) *Service {
	return &Service{
		store: store,
		ads:   ads,
		now:   func() time.Time { return time.Now().UTC() },
		newID: func() (string, error) { id, err := uuid.NewV7(); return id.String(), err },
		log:   log,
	}
}

// messageSentPayload — payload bozor.chat.message_sent. user_id — адресат
// (второй участник); Notification уведомляет его о новом сообщении.
type messageSentPayload struct {
	UserID         string `json:"user_id"`
	ConversationID string `json:"conversation_id"`
	AdID           string `json:"ad_id"`
	SenderID       string `json:"sender_id"`
	MessageID      string `json:"message_id"`
}

// StartConversation создаёт (или возвращает существующий) диалог покупателя с
// продавцом объявления. Инициатор всегда покупатель; продавец — владелец объявления.
func (s *Service) StartConversation(ctx context.Context, adID, buyerID string) (domain.Conversation, error) {
	ad, found, err := s.ads.GetAd(ctx, adID)
	if err != nil {
		return domain.Conversation{}, err
	}
	if !found {
		return domain.Conversation{}, ErrAdNotFound
	}
	if ad.UserID == buyerID {
		return domain.Conversation{}, ErrSelfConversation
	}
	id, err := s.newID()
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("app: генерация id диалога: %w", err)
	}
	now := s.now()
	conv := domain.Conversation{
		ID: id, AdID: adID, BuyerID: buyerID, SellerID: ad.UserID,
		CreatedAt: now, LastMessageAt: now,
	}
	return s.store.EnsureConversation(ctx, conv)
}

// SendMessage отправляет сообщение от участника диалога и публикует
// bozor.chat.message_sent для уведомления адресата. Возвращает также id адресата
// (второго участника) — для realtime-доставки по WebSocket (7.2).
func (s *Service) SendMessage(ctx context.Context, conversationID, senderID, body string) (domain.Message, string, error) {
	if err := domain.ValidateBody(body); err != nil {
		return domain.Message{}, "", err
	}
	conv, found, err := s.store.GetConversation(ctx, conversationID)
	if err != nil {
		return domain.Message{}, "", err
	}
	if !found {
		return domain.Message{}, "", ErrConversationNotFound
	}
	if !conv.Participant(senderID) {
		return domain.Message{}, "", ErrNotParticipant
	}
	recipient := conv.Counterpart(senderID)
	id, err := s.newID()
	if err != nil {
		return domain.Message{}, "", fmt.Errorf("app: генерация id сообщения: %w", err)
	}
	msg := domain.Message{
		ID: id, ConversationID: conversationID, SenderID: senderID,
		Body: strings.TrimSpace(body), CreatedAt: s.now(),
	}
	// Событие адресату. В 7.1 публикуется на каждое сообщение (Notification →
	// Telegram); гейтинг по онлайн-статусу получателя — 7.3.
	ev, err := events.New(events.SubjectChatMessageSent, source, messageSentPayload{
		UserID: recipient, ConversationID: conversationID,
		AdID: conv.AdID, SenderID: senderID, MessageID: id,
	})
	if err != nil {
		return domain.Message{}, "", fmt.Errorf("app: сборка bozor.chat.message_sent: %w", err)
	}
	if err := s.store.InsertMessageWithEvent(ctx, msg, ev); err != nil {
		return domain.Message{}, "", err
	}
	s.log.InfoContext(ctx, "сообщение отправлено",
		slog.String("conversation_id", conversationID), slog.String("sender_id", senderID))
	return msg, recipient, nil
}

// ListConversations возвращает диалоги пользователя.
func (s *Service) ListConversations(ctx context.Context, userID string, limit int) ([]domain.Conversation, error) {
	return s.store.ListConversations(ctx, userID, limit)
}

// ListMessages возвращает историю диалога — только участнику.
func (s *Service) ListMessages(ctx context.Context, conversationID, userID string, limit int) ([]domain.Message, error) {
	conv, found, err := s.store.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrConversationNotFound
	}
	if !conv.Participant(userID) {
		return nil, ErrNotParticipant
	}
	return s.store.ListMessages(ctx, conversationID, limit)
}
