package transport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/auth/internal/app"
	"bozor/services/auth/internal/domain"
)

// telegramSecretHeader — заголовок подлинности вебхука Telegram.
const telegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token" //nolint:gosec // имя заголовка, не секрет

// maxWebhookBody — предел размера тела вебхука (1 MiB).
const maxWebhookBody = 1 << 20

// Bot — исходящие сообщения в Telegram (реализуется telegram.Client).
type Bot interface {
	SendContactRequest(ctx context.Context, chatID int64, text, buttonText string) error
	SendText(ctx context.Context, chatID int64, text string) error
}

// SessionLinker связывает логин-сессию (nonce) с Telegram-пользователем и
// подтверждает её после регистрации контакта (реализуется session.Store).
type SessionLinker interface {
	Link(ctx context.Context, nonce string, telegramUserID int64) error
	Confirm(ctx context.Context, telegramUserID int64, userID string) (nonce string, err error)
}

// WebhookHandler принимает обновления Telegram Bot API и ведёт flow
// request_contact: /start[ <nonce>] → кнопка запроса контакта (и привязка
// deep-link-сессии); contact → регистрация и подтверждение сессии.
type WebhookHandler struct {
	secret   []byte
	app      *app.Service
	bot      Bot
	sessions SessionLinker
	log      *slog.Logger
}

// NewWebhookHandler создаёт обработчик вебхука.
func NewWebhookHandler(secret string, svc *app.Service, bot Bot, sessions SessionLinker, log *slog.Logger) *WebhookHandler {
	return &WebhookHandler{secret: []byte(secret), app: svc, bot: bot, sessions: sessions, log: log}
}

// Handle проверяет secret-token, разбирает обновление и всегда отвечает 2xx
// (Telegram ретраит доставку до успешного статуса); ошибки обработки
// сообщаются пользователю ответным сообщением бота, а не кодом ответа.
func (h *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(telegramSecretHeader)), h.secret) != 1 {
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

	if upd.Message != nil {
		h.dispatch(r.Context(), upd.Message)
	}
	httpx.Respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

// dispatch маршрутизирует сообщение: контакт → регистрация, /start → запрос контакта.
func (h *WebhookHandler) dispatch(ctx context.Context, msg *message) {
	switch {
	case msg.Contact != nil:
		h.handleContact(ctx, msg)
	case strings.HasPrefix(msg.Text, "/start"):
		h.handleStart(ctx, msg)
	}
}

func (h *WebhookHandler) handleStart(ctx context.Context, msg *message) {
	if msg.From == nil {
		return
	}
	// /start <nonce> — пользователь пришёл по deep-link: привязываем сессию.
	// Невалидный/истёкший nonce не мешает регистрации — просто не будет
	// подтверждена web-сессия.
	if nonce := startPayload(msg.Text); nonce != "" && h.sessions != nil {
		if err := h.sessions.Link(ctx, nonce, msg.From.ID); err != nil {
			h.log.InfoContext(ctx, "deep-link nonce не привязан",
				slog.Int64("telegram_user_id", msg.From.ID), slog.String("error", err.Error()))
		}
	}

	m := messagesFor(domain.NormalizeLang(msg.From.LanguageCode))
	if err := h.bot.SendContactRequest(ctx, msg.From.ID, m.askContact, m.contactBtn); err != nil {
		h.log.WarnContext(ctx, "не удалось отправить запрос контакта", slog.String("error", err.Error()))
	}
}

// startPayload извлекает аргумент команды /start ("/start <payload>" → payload;
// без аргумента — пустая строка). Payload deep-link — это nonce логина.
func startPayload(text string) string {
	fields := strings.Fields(text)
	if len(fields) >= 2 {
		return fields[1]
	}
	return ""
}

func (h *WebhookHandler) handleContact(ctx context.Context, msg *message) {
	if msg.From == nil {
		return
	}
	m := messagesFor(domain.NormalizeLang(msg.From.LanguageCode))

	res, err := h.app.RegisterContact(ctx, app.Contact{
		FromID:        msg.From.ID,
		ContactUserID: msg.Contact.UserID,
		PhoneNumber:   msg.Contact.PhoneNumber,
		FirstName:     msg.From.FirstName,
		LastName:      msg.From.LastName,
		Username:      msg.From.Username,
		LanguageCode:  msg.From.LanguageCode,
	})
	if err != nil {
		h.replyOnError(ctx, msg.From.ID, m, err)
		return
	}

	h.log.InfoContext(ctx, "контакт обработан",
		slog.Int64("telegram_user_id", msg.From.ID), slog.Bool("created", res.Created))

	// Если пользователь пришёл по deep-link — подтверждаем его web-сессию.
	confirmText := m.confirmed
	if h.sessions != nil {
		nonce, err := h.sessions.Confirm(ctx, msg.From.ID, res.UserID)
		switch {
		case err != nil:
			h.log.WarnContext(ctx, "не удалось подтвердить логин-сессию", slog.String("error", err.Error()))
		case nonce != "":
			confirmText = m.loggedIn // вход по deep-link — просим вернуться в приложение
		}
	}

	if err := h.bot.SendText(ctx, msg.From.ID, confirmText); err != nil {
		h.log.WarnContext(ctx, "не удалось отправить подтверждение", slog.String("error", err.Error()))
	}
}

// replyOnError отправляет пользователю локализованное сообщение об ошибке.
func (h *WebhookHandler) replyOnError(ctx context.Context, chatID int64, m botMessages, err error) {
	var text string
	switch apperr.KindOf(err) {
	case apperr.KindForbidden:
		text = m.notOwned
	case apperr.KindInvalid:
		text = m.invalidPhone
	default:
		h.log.ErrorContext(ctx, "ошибка обработки контакта", slog.String("error", err.Error()))
		text = m.genericErr
	}
	if sendErr := h.bot.SendText(ctx, chatID, text); sendErr != nil {
		h.log.WarnContext(ctx, "не удалось отправить сообщение об ошибке", slog.String("error", sendErr.Error()))
	}
}

// decodeUpdate читает тело как обновление Telegram (толерантный ридer:
// неизвестные поля игнорируются — payload Telegram шире нашей модели).
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
