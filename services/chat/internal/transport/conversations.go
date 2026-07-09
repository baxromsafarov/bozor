package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/chat/internal/app"
	"bozor/services/chat/internal/domain"
)

const (
	maxBodyBytes    = 16 << 10 // 16 KiB — сообщение до 4000 рун + JSON-обвязка
	defaultMsgLimit = 30
	maxMsgLimit     = 100
	defaultConvLim  = 50
	maxConvLimit    = 100
)

// Chat — use-cases чата (реализуется app.Service). SendMessage возвращает также
// id адресата (используется WS-транспортом для realtime-доставки; REST его игнорирует).
type Chat interface {
	StartConversation(ctx context.Context, adID, buyerID string) (domain.Conversation, error)
	SendMessage(ctx context.Context, conversationID, senderID, body string) (domain.Message, string, error)
	ListConversations(ctx context.Context, userID string, limit int) ([]domain.Conversation, error)
	ListMessages(ctx context.Context, conversationID, userID string, limit int) ([]domain.Message, error)
	MarkRead(ctx context.Context, conversationID, readerID string) (int, error)
}

// ConversationHandler обслуживает REST-историю и отправку сообщений чата.
type ConversationHandler struct {
	chat Chat
	log  *slog.Logger
}

// NewConversationHandler создаёт обработчик диалогов.
func NewConversationHandler(chat Chat, log *slog.Logger) *ConversationHandler {
	return &ConversationHandler{chat: chat, log: log}
}

type startRequest struct {
	AdID string `json:"ad_id"`
}

type sendRequest struct {
	Body string `json:"body"`
}

type conversationDTO struct {
	ID            string `json:"id"`
	AdID          string `json:"ad_id"`
	BuyerID       string `json:"buyer_id"`
	SellerID      string `json:"seller_id"`
	UnreadCount   int    `json:"unread_count"`
	LastMessageAt string `json:"last_message_at"`
	CreatedAt     string `json:"created_at"`
}

type messageDTO struct {
	ID        string  `json:"id"`
	SenderID  string  `json:"sender_id"`
	Body      string  `json:"body"`
	CreatedAt string  `json:"created_at"`
	ReadAt    *string `json:"read_at,omitempty"`
}

// Start создаёт (или возвращает) диалог покупателя с продавцом объявления.
func (h *ConversationHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.AdID == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_ad_id",
			"Требуется идентификатор объявления", "E'lon identifikatori talab qilinadi"))
		return
	}
	conv, err := h.chat.StartConversation(r.Context(), req.AdID, authx.UserID(r.Context()))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toConversationDTO(conv))
}

// List отдаёт диалоги текущего пользователя.
func (h *ConversationHandler) List(w http.ResponseWriter, r *http.Request) {
	convs, err := h.chat.ListConversations(r.Context(), authx.UserID(r.Context()),
		clamp(r.URL.Query().Get("limit"), defaultConvLim, maxConvLimit))
	if err != nil {
		h.internal(w, r, err)
		return
	}
	out := make([]conversationDTO, 0, len(convs))
	for _, c := range convs {
		out = append(out, toConversationDTO(c))
	}
	httpx.Respond(w, http.StatusOK, map[string]any{"conversations": out})
}

// Messages отдаёт историю диалога — только участнику.
func (h *ConversationHandler) Messages(w http.ResponseWriter, r *http.Request) {
	msgs, err := h.chat.ListMessages(r.Context(), chi.URLParam(r, "id"), authx.UserID(r.Context()),
		clamp(r.URL.Query().Get("limit"), defaultMsgLimit, maxMsgLimit))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := make([]messageDTO, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toMessageDTO(m))
	}
	httpx.Respond(w, http.StatusOK, map[string]any{"messages": out})
}

// Read помечает сообщения собеседника в диалоге прочитанными.
func (h *ConversationHandler) Read(w http.ResponseWriter, r *http.Request) {
	n, err := h.chat.MarkRead(r.Context(), chi.URLParam(r, "id"), authx.UserID(r.Context()))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, map[string]int{"marked": n})
}

// Send отправляет сообщение в диалог от текущего пользователя.
func (h *ConversationHandler) Send(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	msg, _, err := h.chat.SendMessage(r.Context(), chi.URLParam(r, "id"), authx.UserID(r.Context()), req.Body)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toMessageDTO(msg))
}

func toConversationDTO(c domain.Conversation) conversationDTO {
	return conversationDTO{
		ID: c.ID, AdID: c.AdID, BuyerID: c.BuyerID, SellerID: c.SellerID,
		UnreadCount:   c.UnreadCount,
		LastMessageAt: c.LastMessageAt.UTC().Format(time.RFC3339),
		CreatedAt:     c.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func toMessageDTO(m domain.Message) messageDTO {
	dto := messageDTO{
		ID: m.ID, SenderID: m.SenderID, Body: m.Body,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
	}
	if m.ReadAt != nil {
		s := m.ReadAt.UTC().Format(time.RFC3339)
		dto.ReadAt = &s
	}
	return dto
}

// writeError переводит доменные ошибки чата в ответы RFC 7807.
func (h *ConversationHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, app.ErrAdNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "ad_not_found",
			"Объявление не найдено", "E'lon topilmadi"))
	case errors.Is(err, app.ErrConversationNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "conversation_not_found",
			"Диалог не найден", "Suhbat topilmadi"))
	case errors.Is(err, app.ErrNotParticipant):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindForbidden, "forbidden",
			"Нет доступа к диалогу", "Suhbatga ruxsat yo'q"))
	case errors.Is(err, app.ErrSelfConversation):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "self_conversation",
			"Нельзя написать самому себе", "O'zingizga yozib bo'lmaydi"))
	case errors.Is(err, domain.ErrEmptyBody):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "empty_body",
			"Пустое сообщение", "Bo'sh xabar"))
	case errors.Is(err, domain.ErrBodyTooLong):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "body_too_long",
			"Сообщение слишком длинное", "Xabar juda uzun"))
	default:
		h.internal(w, r, err)
	}
}

func (h *ConversationHandler) internal(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "ошибка чата", slog.String("error", err.Error()))
	httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "chat_failed",
		"Внутренняя ошибка", "Ichki xatolik"))
}

// clamp разбирает limit из query и ограничивает диапазоном [1, max] с дефолтом.
func clamp(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
