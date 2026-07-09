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
	"bozor/pkg/shared/events"

	"bozor/services/chat/internal/app"
	"bozor/services/chat/internal/domain"
)

const testKey = "test-signing-key-0123456789"

// loopback — внутрипроцессный backplane: Publish синхронно вызывает handler'ы
// подписчиков (как NATS на одном узле). RespondPresence — no-op (присутствие в
// тестах задаётся fakePresence, минуя шину).
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

func (l *loopback) RespondPresence(string) (Subscription, error) { return noopSub{}, nil }

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

// fakeStore — минимальный app.Store для интеграции WS: GetConversation отдаёт
// заданный диалог, InsertMessage/MarkRead — no-op с настраиваемым результатом.
type fakeStore struct {
	conv  domain.Conversation
	found bool
	markN int64
}

func (f *fakeStore) EnsureConversation(_ context.Context, c domain.Conversation) (domain.Conversation, error) {
	return c, nil
}
func (f *fakeStore) GetConversation(_ context.Context, _ string) (domain.Conversation, bool, error) {
	return f.conv, f.found, nil
}
func (f *fakeStore) ListConversations(_ context.Context, _ string, _ int) ([]domain.Conversation, error) {
	return nil, nil
}
func (f *fakeStore) ListMessages(_ context.Context, _ string, _ int) ([]domain.Message, error) {
	return nil, nil
}
func (f *fakeStore) InsertMessage(_ context.Context, _ domain.Message, _ *events.Envelope) error {
	return nil
}
func (f *fakeStore) MarkRead(_ context.Context, _, _ string, _ time.Time) (int64, error) {
	return f.markN, nil
}

type fakePresence struct{ online bool }

func (f fakePresence) Online(context.Context, string) (bool, error) { return f.online, nil }

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mint(t *testing.T, userID string) string {
	t.Helper()
	tok, _, _, err := authx.NewSigner([]byte(testKey), "auth", time.Hour).Sign(userID, []string{"user"})
	require.NoError(t, err)
	return tok
}

// wsServer поднимает /ws с реальными app.Service + Hub + Delivery поверх loopback.
func wsServer(t *testing.T, store *fakeStore, online bool) *httptest.Server {
	t.Helper()
	hub := NewHub(newLoopback(), testLog())
	svc := app.NewService(store, nil, fakePresence{online: online}, NewDelivery(hub), testLog())
	h := NewHandler(context.Background(), hub, svc, []byte(testKey), testLog())
	srv := httptest.NewServer(http.HandlerFunc(h.ServeWS))
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server, token string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token
}

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

func readFrameJSON(t *testing.T, ctx context.Context, c *websocket.Conn) map[string]any {
	t.Helper()
	_, data, err := c.Read(ctx)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func convOf(buyer, seller string) domain.Conversation {
	return domain.Conversation{ID: "c-1", AdID: "ad-1", BuyerID: buyer, SellerID: seller}
}

func TestWS_RejectsBadToken(t *testing.T) {
	srv := wsServer(t, &fakeStore{}, false)
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
	srv := wsServer(t, &fakeStore{conv: convOf(buyer, seller), found: true}, true)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

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
	time.Sleep(200 * time.Millisecond) // дать продавцу подписаться

	buyerConn := dial(t, ctx, srv, buyer)
	frame, _ := json.Marshal(map[string]string{"conversation_id": "c-1", "body": "привет"})
	require.NoError(t, buyerConn.Write(ctx, websocket.MessageText, frame))

	ack := readFrameJSON(t, ctx, buyerConn)
	assert.Equal(t, "ack", ack["type"])

	select {
	case m := <-recv:
		assert.Equal(t, "message", m["type"])
		assert.Equal(t, buyer, m["sender_id"])
		assert.Equal(t, "привет", m["body"])
	case <-time.After(3 * time.Second):
		t.Fatal("получатель не получил сообщение")
	}
}

func TestWS_ReadReceipt_NotifiesCounterpart(t *testing.T) {
	buyer, seller := "buyer-1", "seller-1"
	// markN>0 → MarkRead уведомит собеседника (buyer).
	srv := wsServer(t, &fakeStore{conv: convOf(buyer, seller), found: true, markN: 2}, true)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	buyerConn := dial(t, ctx, srv, buyer)
	recv := make(chan map[string]any, 2)
	go func() {
		_, data, err := buyerConn.Read(ctx)
		if err != nil {
			return
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		recv <- m
	}()
	time.Sleep(200 * time.Millisecond)

	// Продавец помечает диалог прочитанным.
	sellerConn := dial(t, ctx, srv, seller)
	frame, _ := json.Marshal(map[string]string{"type": "read", "conversation_id": "c-1"})
	require.NoError(t, sellerConn.Write(ctx, websocket.MessageText, frame))

	select {
	case m := <-recv:
		assert.Equal(t, "read", m["type"], "автор получает уведомление о прочтении")
		assert.Equal(t, seller, m["reader_id"])
		assert.Equal(t, "c-1", m["conversation_id"])
	case <-time.After(3 * time.Second):
		t.Fatal("автор не получил отметку о прочтении")
	}
}

func TestWS_SendToForeignConversation_ReturnsErrorFrame(t *testing.T) {
	// Диалог без отправителя среди участников → ErrNotParticipant.
	srv := wsServer(t, &fakeStore{conv: convOf("buyer-1", "seller-1"), found: true}, false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := dial(t, ctx, srv, "stranger")
	frame, _ := json.Marshal(map[string]string{"conversation_id": "c-1", "body": "привет"})
	require.NoError(t, conn.Write(ctx, websocket.MessageText, frame))

	e := readFrameJSON(t, ctx, conn)
	assert.Equal(t, "error", e["type"])
	assert.Equal(t, "forbidden", e["code"])
}
