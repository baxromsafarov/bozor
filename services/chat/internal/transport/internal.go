package transport

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/chat/internal/app"
)

// InternalHandler отдаёт содержимое сообщения для модерации (внутренняя сеть,
// без аутентификации — как /internal/ads у Listing; наружу закрыт nginx/gateway).
type InternalHandler struct {
	chat Chat
	log  *slog.Logger
}

// NewInternalHandler создаёт внутренний обработчик.
func NewInternalHandler(chat Chat, log *slog.Logger) *InternalHandler {
	return &InternalHandler{chat: chat, log: log}
}

type internalMessageDTO struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	SenderID       string `json:"sender_id"`
	Body           string `json:"body"`
	Blocked        bool   `json:"blocked"`
	CreatedAt      string `json:"created_at"`
}

// Message отдаёт сообщение по id (для очереди жалоб модерации).
func (h *InternalHandler) Message(w http.ResponseWriter, r *http.Request) {
	msg, err := h.chat.GetMessage(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, app.ErrMessageNotFound) {
			httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "message_not_found",
				"Сообщение не найдено", "Xabar topilmadi"))
			return
		}
		h.log.ErrorContext(r.Context(), "внутреннее чтение сообщения", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "chat_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
		return
	}
	httpx.Respond(w, http.StatusOK, internalMessageDTO{
		ID: msg.ID, ConversationID: msg.ConversationID, SenderID: msg.SenderID,
		Body: msg.Body, Blocked: msg.Blocked, CreatedAt: msg.CreatedAt.UTC().Format(time.RFC3339),
	})
}
