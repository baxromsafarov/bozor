package ws

import (
	"log/slog"
	"sync"

	"github.com/coder/websocket"
)

// sendBuffer — размер буфера исходящих сообщений на соединение. Переполнение
// (медленный клиент) закрывает сокет: клиент переподключится и перечитает историю.
const sendBuffer = 32

// Conn — одно WebSocket-соединение пользователя с буфером исходящих сообщений.
// Запись в сокет сериализуется одной горутиной writer (см. handler); enqueue
// лишь кладёт кадр в буфер.
type Conn struct {
	ws       *websocket.Conn
	send     chan []byte
	closeOne sync.Once
}

func newConn(c *websocket.Conn) *Conn {
	return &Conn{ws: c, send: make(chan []byte, sendBuffer)}
}

// enqueue кладёт кадр в буфер соединения; при переполнении закрывает соединение
// как медленное (неблокирующе — вызывается из backplane-горутины Hub).
func (c *Conn) enqueue(payload []byte) {
	select {
	case c.send <- payload:
	default:
		c.closeSlow()
	}
}

func (c *Conn) closeSlow() {
	c.closeOne.Do(func() {
		_ = c.ws.Close(websocket.StatusPolicyViolation, "медленный клиент")
	})
}

// userEntry — набор локальных соединений пользователя и его подписка backplane.
type userEntry struct {
	conns map[*Conn]struct{}
	sub   Subscription
}

// Hub — локальный реестр соединений реплики с межрепличной доставкой. Одна
// подписка backplane на пользователя (refcount по числу соединений); входящие из
// backplane кадры пишутся во все локальные соединения пользователя.
type Hub struct {
	mu    sync.Mutex
	bp    Backplane
	log   *slog.Logger
	users map[string]*userEntry
}

// NewHub создаёт Hub поверх backplane.
func NewHub(bp Backplane, log *slog.Logger) *Hub {
	return &Hub{bp: bp, log: log, users: make(map[string]*userEntry)}
}

// Add регистрирует соединение пользователя; при первом соединении подписывается
// на backplane (chat.rt.<userID>).
func (h *Hub) Add(userID string, c *Conn) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	e, ok := h.users[userID]
	if !ok {
		sub, err := h.bp.Subscribe(userID, func(data []byte) { h.deliver(userID, data) })
		if err != nil {
			return err
		}
		e = &userEntry{conns: make(map[*Conn]struct{}), sub: sub}
		h.users[userID] = e
	}
	e.conns[c] = struct{}{}
	return nil
}

// Remove удаляет соединение; при отключении последнего соединения пользователя
// отменяет подписку backplane.
func (h *Hub) Remove(userID string, c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	e, ok := h.users[userID]
	if !ok {
		return
	}
	delete(e.conns, c)
	if len(e.conns) == 0 {
		if err := e.sub.Unsubscribe(); err != nil {
			h.log.Warn("отмена подписки backplane", slog.String("user_id", userID), slog.String("error", err.Error()))
		}
		delete(h.users, userID)
	}
}

// Publish отправляет кадр адресату userID через backplane (доставят все реплики
// с его подпиской, включая текущую).
func (h *Hub) Publish(userID string, payload []byte) error {
	return h.bp.Publish(userID, payload)
}

// deliver пишет кадр из backplane во все локальные соединения пользователя.
func (h *Hub) deliver(userID string, payload []byte) {
	h.mu.Lock()
	e, ok := h.users[userID]
	if !ok {
		h.mu.Unlock()
		return
	}
	conns := make([]*Conn, 0, len(e.conns))
	for c := range e.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	for _, c := range conns {
		c.enqueue(payload)
	}
}

// LocalConnCount возвращает число локальных соединений пользователя (для тестов/метрик).
func (h *Hub) LocalConnCount(userID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.users[userID]; ok {
		return len(e.conns)
	}
	return 0
}
