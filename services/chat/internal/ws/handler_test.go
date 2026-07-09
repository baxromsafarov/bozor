package ws

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/authx"

	"bozor/services/chat/internal/app"
	"bozor/services/chat/internal/domain"
)

const testKey = "test-signing-key-0123456789"

// loopback — внутрипроцессный backplane: Publish синхронно вызывает handler'ы
// подписчиков (как NATS на одном узле). Позволяет тестировать доставку без сети.
type loopback struct {
	mu   sync.Mutex
	subs map[string][]*func([]byte)
}

func newLoopback() *loopback { return &loopback{subs: make(map[string][]*func([]byte))} }

func (l *loopback) Publish(userID string, payload []byte) error {
	l.mu.Lock()
	hs := append([]*func([]byte){}, l.subs[userID]...)
	l.mu.Unlock()
	for _, h := range hs {
		(*h)(payload)
	}
	return nil
}

func (l *loopback) Subscribe(userID string, handler func([]byte)) (Subscription, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	hp := &handler
	l.subs[userID] = append(l.subs[userID], hp)
	return &loopbackSub{l: l, userID: userID, hp: hp}, nil
}

type loopbackSub struct {
	l      *loopback
	userID string
	hp     *func([]byte)
}

func (s *loopbackSub) Unsubscribe() error {
	s.l.mu.Lock()
	defer s.l.mu.Unlock()
	cur := s.l.subs[s.userID]
	for i, h := range cur {
		if h == s.hp {
			s.l.subs[s.userID] = append(cur[:i], cur[i+1:]...)
			break
		}
	}
	return nil
}

// fakeSender — заглушка отправки: сохраняет вызов и возвращает готовое сообщение.
type fakeSender struct {
	recipient string
	err       error
	gotConv   string
	gotSender string
	gotBody   string
}

func (f *fakeSender) SendMessage(_ context.Context, convID, senderID, body string) (domain.Message, string, error) {
	f.gotConv, f.gotSender, f.gotBody = convID, senderID, body
	if f.err != nil {
		return domain.Message{}, "", f.err
	}
	return domain.Message{ID: "m-1", ConversationID: convID, SenderID: senderID, Body: body,
		CreatedAt: time.Unix(1700000000, 0).UTC()}, f.recipient, nil
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mint(t *testing.T, userID string) string {
	t.Helper()
	tok, _, _, err := authx.NewSigner([]byte(testKey), "auth", time.Hour).Sign(userID, []string{"user"})
	require.NoError(t, err)
	return tok
}

// wsServer поднимает /ws на httptest-сервере с реальным Hub поверх loopback-backplane.
func wsServer(t *testing.T, sender Sender) *httptest.Server {
	t.Helper()
	hub := NewHub(newLoopback(), testLog())
	h := NewHandler(context.Background(), hub, sender, []byte(testKey), testLog())
	srv := httptest.NewServer(http.HandlerFunc(h.ServeWS))
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server, token string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token
}

// dial подключается к WS-серверу и закрывает соединение по завершении теста.
func dial(t *testing.T, ctx context.Context, srv *httptest.Server, userID string) *websocket.Conn {
	t.Helper()
	c, resp, err := websocket.Dial(ctx, wsURL(srv, mint(t, userID)), nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func TestWS_RejectsBadToken(t *testing.T) {
	srv := wsServer(t, &fakeSender{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, wsURL(srv, "garbage"), nil)
	require.Error(t, err, "рукопожатие должно провалиться")
	require.NotNil(t, resp)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestWS_SendDeliversToRecipientAndAcksSender(t *testing.T) {
	buyer, seller := "buyer-1", "seller-1"
	sender := &fakeSender{recipient: seller}
	srv := wsServer(t, sender)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// Получатель (продавец) подключается и слушает.
	sellerConn := dial(t, ctx, srv, seller)

	recv := make(chan map[string]any, 2)
	go func() {
		_, data, err := sellerConn.Read(ctx)
		if err != nil {
			return
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		recv <- m
	}()

	// Дать продавцу подписаться на backplane до отправки.
	time.Sleep(200 * time.Millisecond)

	// Отправитель (покупатель) подключается и шлёт сообщение.
	buyerConn := dial(t, ctx, srv, buyer)

	frame, _ := json.Marshal(map[string]string{"conversation_id": "c-1", "body": "привет"})
	require.NoError(t, buyerConn.Write(ctx, websocket.MessageText, frame))

	// Покупатель получает ack.
	_, ackData, err := buyerConn.Read(ctx)
	require.NoError(t, err)
	var ack map[string]any
	require.NoError(t, json.Unmarshal(ackData, &ack))
	assert.Equal(t, "ack", ack["type"])
	assert.Equal(t, "m-1", ack["id"])

	// Отправка прошла через Sender с корректными параметрами.
	assert.Equal(t, "c-1", sender.gotConv)
	assert.Equal(t, buyer, sender.gotSender)
	assert.Equal(t, "привет", sender.gotBody)

	// Продавец получает кадр сообщения (через backplane).
	select {
	case m := <-recv:
		assert.Equal(t, "message", m["type"])
		assert.Equal(t, buyer, m["sender_id"])
		assert.Equal(t, "привет", m["body"])
	case <-time.After(3 * time.Second):
		t.Fatal("получатель не получил сообщение")
	}
}

func TestWS_SendError_ReturnsErrorFrame(t *testing.T) {
	sender := &fakeSender{err: app.ErrNotParticipant}
	srv := wsServer(t, sender)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := dial(t, ctx, srv, "stranger")

	frame, _ := json.Marshal(map[string]string{"conversation_id": "c-1", "body": "привет"})
	require.NoError(t, conn.Write(ctx, websocket.MessageText, frame))

	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	var e map[string]any
	require.NoError(t, json.Unmarshal(data, &e))
	assert.Equal(t, "error", e["type"])
	assert.Equal(t, "forbidden", e["code"])
}
