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
	ErrRateLimited          = domain.ErrRateLimited
	ErrUserBanned           = domain.ErrUserBanned
	ErrMessageNotFound      = domain.ErrMessageNotFound
)

// Store — доступ к диалогам/сообщениям (реализуется repo.Repo).
type Store interface {
	EnsureConversation(ctx context.Context, conv domain.Conversation) (domain.Conversation, error)
	GetConversation(ctx context.Context, id string) (domain.Conversation, bool, error)
	ListConversations(ctx context.Context, userID string, limit int) ([]domain.Conversation, error)
	ListMessages(ctx context.Context, conversationID string, limit int) ([]domain.Message, error)
	GetMessage(ctx context.Context, id string) (domain.Message, bool, error)
	InsertMessage(ctx context.Context, msg domain.Message, ev *events.Envelope) error
	MarkRead(ctx context.Context, conversationID, readerID string, at time.Time) (int64, error)
	IsBanned(ctx context.Context, userID string) (bool, error)
}

// Limiter — per-user лимит частоты отправки (реализуется ratelimit.UserLimiter).
type Limiter interface {
	Allow(userID string) bool
}

// AdSource — чтение объявления (продавец = владелец) из Listing.
type AdSource interface {
	GetAd(ctx context.Context, id string) (domain.AdView, bool, error)
}

// Presence — проверка онлайн-статуса получателя (кросс-реплично через backplane).
type Presence interface {
	Online(ctx context.Context, userID string) (bool, error)
}

// Realtime — доставка кадров подключённым клиентам через backplane.
type Realtime interface {
	DeliverMessage(recipientID string, msg domain.Message)
	NotifyRead(recipientID string, r domain.ReadReceipt)
}

// Service — use-cases чата.
type Service struct {
	store    Store
	ads      AdSource
	presence Presence
	rt       Realtime
	limiter  Limiter
	now      func() time.Time
	newID    func() (string, error)
	log      *slog.Logger
}

// NewService создаёт сервис чата. presence/rt отвечают за онлайн-гейтинг
// Telegram-уведомления и realtime-доставку; limiter — лимит частоты отправки.
func NewService(store Store, ads AdSource, presence Presence, rt Realtime, limiter Limiter, log *slog.Logger) *Service {
	return &Service{
		store:    store,
		ads:      ads,
		presence: presence,
		rt:       rt,
		limiter:  limiter,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    func() (string, error) { id, err := uuid.NewV7(); return id.String(), err },
		log:      log,
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
	banned, err := s.store.IsBanned(ctx, buyerID)
	if err != nil {
		return domain.Conversation{}, err
	}
	if banned {
		return domain.Conversation{}, ErrUserBanned
	}
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
	if s.limiter != nil && !s.limiter.Allow(senderID) {
		return domain.Message{}, "", ErrRateLimited
	}
	banned, err := s.store.IsBanned(ctx, senderID)
	if err != nil {
		return domain.Message{}, "", err
	}
	if banned {
		return domain.Message{}, "", ErrUserBanned
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

	// Онлайн-гейтинг (7.3): получателю онлайн доставляем по WS без Telegram; иначе
	// публикуем bozor.chat.message_sent (Notification → Telegram). Ошибка проверки
	// присутствия трактуется как «оффлайн» (лучше лишнее уведомление, чем немое).
	online := false
	if s.presence != nil {
		online, err = s.presence.Online(ctx, recipient)
		if err != nil {
			s.log.WarnContext(ctx, "проверка присутствия", slog.String("error", err.Error()))
			online = false
		}
	}

	var ev *events.Envelope
	if !online {
		e, err := events.New(events.SubjectChatMessageSent, source, messageSentPayload{
			UserID: recipient, ConversationID: conversationID,
			AdID: conv.AdID, SenderID: senderID, MessageID: id,
		})
		if err != nil {
			return domain.Message{}, "", fmt.Errorf("app: сборка bozor.chat.message_sent: %w", err)
		}
		ev = &e
	}

	if err := s.store.InsertMessage(ctx, msg, ev); err != nil {
		return domain.Message{}, "", err
	}

	// Realtime-доставка получателю (fire-and-forget; если не подключён — no-op).
	if s.rt != nil {
		s.rt.DeliverMessage(recipient, msg)
	}
	s.log.InfoContext(ctx, "сообщение отправлено",
		slog.String("conversation_id", conversationID), slog.String("sender_id", senderID),
		slog.Bool("recipient_online", online))
	return msg, recipient, nil
}

// MarkRead помечает прочитанными сообщения собеседника в диалоге и уведомляет его
// (realtime), что сообщения прочитаны. Возвращает число помеченных. Доступно
// только участнику диалога.
func (s *Service) MarkRead(ctx context.Context, conversationID, readerID string) (int, error) {
	conv, found, err := s.store.GetConversation(ctx, conversationID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, ErrConversationNotFound
	}
	if !conv.Participant(readerID) {
		return 0, ErrNotParticipant
	}
	at := s.now()
	n, err := s.store.MarkRead(ctx, conversationID, readerID, at)
	if err != nil {
		return 0, err
	}
	// Уведомляем автора прочитанных сообщений (второго участника).
	if n > 0 && s.rt != nil {
		s.rt.NotifyRead(conv.Counterpart(readerID), domain.ReadReceipt{
			ConversationID: conversationID, ReaderID: readerID, At: at,
		})
	}
	return int(n), nil
}

// ListConversations возвращает диалоги пользователя.
func (s *Service) ListConversations(ctx context.Context, userID string, limit int) ([]domain.Conversation, error) {
	return s.store.ListConversations(ctx, userID, limit)
}

// GetMessage возвращает сообщение по id (внутреннее чтение модерацией). Тело
// снятого сообщения уже скрыто на уровне repo.
func (s *Service) GetMessage(ctx context.Context, id string) (domain.Message, error) {
	m, found, err := s.store.GetMessage(ctx, id)
	if err != nil {
		return domain.Message{}, err
	}
	if !found {
		return domain.Message{}, ErrMessageNotFound
	}
	return m, nil
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
