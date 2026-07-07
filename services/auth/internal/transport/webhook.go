package transport

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"
)

// telegramSecretHeader — заголовок, которым Telegram подтверждает подлинность
// вебхука (значение задаётся при setWebhook).
const telegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token" //nolint:gosec // имя заголовка, не секрет

// maxWebhookBody — предел размера тела вебхука (1 MiB).
const maxWebhookBody = 1 << 20

// WebhookHandler принимает обновления Telegram Bot API.
type WebhookHandler struct {
	secret []byte
	log    *slog.Logger
}

// NewWebhookHandler создаёт обработчик вебхука с ожидаемым secret-token.
func NewWebhookHandler(secret string, log *slog.Logger) *WebhookHandler {
	return &WebhookHandler{secret: []byte(secret), log: log}
}

// Handle проверяет secret-token и принимает обновление. На Stage 1.1 — скелет:
// подтверждение подлинности запроса и приём тела; полная обработка контакта
// (проверка владения, upsert пользователя, связывание с nonce, выпуск токенов)
// добавляется на Stage 1.2+.
func (h *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	got := r.Header.Get(telegramSecretHeader)
	if subtle.ConstantTimeCompare([]byte(got), h.secret) != 1 {
		h.log.WarnContext(r.Context(), "вебхук Telegram с неверным secret-token отклонён")
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "invalid_webhook_secret",
			"Недействительный секрет вебхука", "Yaroqsiz webhook maxfiy kaliti"))
		return
	}

	upd, err := decodeUpdate(w, r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	hasContact := upd.Message != nil && upd.Message.Contact != nil
	h.log.InfoContext(r.Context(), "получено обновление Telegram",
		slog.Int64("update_id", upd.UpdateID),
		slog.Bool("has_contact", hasContact),
	)

	// Telegram ретраит доставку, пока не получит 2xx.
	httpx.Respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

// decodeUpdate читает тело как обновление Telegram. Толерантный ридер:
// неизвестные поля игнорируются (payload Telegram шире нашей модели).
func decodeUpdate(w http.ResponseWriter, r *http.Request) (*update, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)
	var upd update
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		return nil, apperr.Wrap(err, apperr.KindInvalid, "invalid_update",
			"Некорректное тело обновления", "Yaroqsiz yangilanish tanasi")
	}
	return &upd, nil
}

// update — минимальная модель обновления Telegram (только нужные поля).
type update struct {
	UpdateID int64    `json:"update_id"`
	Message  *message `json:"message,omitempty"`
}

type message struct {
	MessageID int64    `json:"message_id"`
	From      *tgUser  `json:"from,omitempty"`
	Contact   *contact `json:"contact,omitempty"`
	Text      string   `json:"text,omitempty"`
}

type tgUser struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name,omitempty"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

type contact struct {
	PhoneNumber string `json:"phone_number"`
	UserID      int64  `json:"user_id,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
}
