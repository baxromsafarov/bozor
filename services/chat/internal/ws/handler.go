package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/chat/internal/app"
	"bozor/services/chat/internal/domain"
)

const (
	bearerPrefix = "Bearer "
	writeTimeout = 5 * time.Second
)

// App — use-cases чата, вызываемые по WS (реализуется app.Service). SendMessage
// сам гейтит Telegram-уведомление по присутствию и доставляет получателю realtime;
// обработчик лишь подтверждает отправителю.
type App interface {
	SendMessage(ctx context.Context, conversationID, senderID, body string) (domain.Message, string, error)
	MarkRead(ctx context.Context, conversationID, readerID string) (int, error)
}

// Handler обслуживает WebSocket-эндпоинт /ws: аутентифицирует соединение по JWT
// (эндпоинт идёт мимо gateway), регистрирует его в Hub и обрабатывает кадры.
type Handler struct {
	hub     *Hub
	app     App
	key     []byte
	baseCtx context.Context
	log     *slog.Logger
}

// NewHandler создаёт WS-обработчик. baseCtx — контекст жизни сервиса: его отмена
// (graceful shutdown) закрывает активные соединения.
func NewHandler(baseCtx context.Context, hub *Hub, application App, key []byte, log *slog.Logger) *Handler {
	return &Handler{hub: hub, app: application, key: key, baseCtx: baseCtx, log: log}
}

// ServeWS аутентифицирует и апгрейдит соединение, затем качает кадры до закрытия.
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	claims, err := authx.Parse(h.key, token(r))
	if err != nil {
		// До апгрейда можно ответить обычным HTTP (401).
		httpx.WriteProblem(w, r, err)
		return
	}
	userID := claims.Subject

	// Auth по JWT (bearer/query), не по cookie, поэтому CSWSH неактуален —
	// проверку Origin отключаем (TLS-край — nginx). Токен нельзя подделать.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()

	conn := newConn(c)
	if err := h.hub.Add(userID, conn); err != nil {
		h.log.ErrorContext(r.Context(), "подписка backplane", slog.String("error", err.Error()))
		_ = c.Close(websocket.StatusInternalError, "backplane")
		return
	}
	defer h.hub.Remove(userID, conn)

	// Контекст соединения отменяется при закрытии сокета ИЛИ остановке сервиса.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		select {
		case <-h.baseCtx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	go h.writeLoop(ctx, cancel, c, conn)
	h.readLoop(ctx, userID, conn, c)
}

// writeLoop сериализует запись в сокет: единственный писатель тянет буфер.
func (h *Handler) writeLoop(ctx context.Context, cancel context.CancelFunc, c *websocket.Conn, conn *Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-conn.send:
			wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
			err := c.Write(wctx, websocket.MessageText, msg)
			wcancel()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

// readLoop читает клиентские кадры (отправка сообщений) до ошибки/закрытия.
func (h *Handler) readLoop(ctx context.Context, userID string, conn *Conn, c *websocket.Conn) {
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return // закрытие сокета или отмена контекста
		}
		h.onFrame(ctx, userID, conn, data)
	}
}

// onFrame обрабатывает один клиентский кадр: отправку сообщения или отметку
// прочтения. Доставку получателю (realtime + гейтинг Telegram) выполняет App.
func (h *Handler) onFrame(ctx context.Context, userID string, conn *Conn, data []byte) {
	var in inbound
	if err := json.Unmarshal(data, &in); err != nil {
		conn.enqueue(errorFrame("bad_frame", "неверный формат кадра"))
		return
	}
	if in.Type == frameRead {
		if _, err := h.app.MarkRead(ctx, in.ConversationID, userID); err != nil {
			code, text := classify(err)
			conn.enqueue(errorFrame(code, text))
		}
		return
	}
	msg, _, err := h.app.SendMessage(ctx, in.ConversationID, userID, in.Body)
	if err != nil {
		code, text := classify(err)
		conn.enqueue(errorFrame(code, text))
		return
	}
	conn.enqueue(ackFrame(msg))
}

// token извлекает JWT из query-параметра token или заголовка Authorization.
func token(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, bearerPrefix) {
		return strings.TrimSpace(authz[len(bearerPrefix):])
	}
	return ""
}

// classify переводит доменную ошибку отправки в код и текст кадра ошибки.
func classify(err error) (code, text string) {
	switch {
	case errors.Is(err, app.ErrConversationNotFound):
		return "conversation_not_found", "диалог не найден"
	case errors.Is(err, app.ErrNotParticipant):
		return "forbidden", "нет доступа к диалогу"
	case errors.Is(err, domain.ErrEmptyBody):
		return "empty_body", "пустое сообщение"
	case errors.Is(err, domain.ErrBodyTooLong):
		return "body_too_long", "сообщение слишком длинное"
	default:
		return "internal", "внутренняя ошибка"
	}
}
