package ws

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBackplane фиксирует подписки/публикации и позволяет тесту инициировать
// доставку, вызвав сохранённый handler подписки.
type fakeBackplane struct {
	mu          sync.Mutex
	handlers    map[string]func([]byte) // userID → deliver-callback
	subscribes  int
	unsubscribe int
	published   []publishRec
}

type publishRec struct {
	userID  string
	payload []byte
}

func newFakeBackplane() *fakeBackplane {
	return &fakeBackplane{handlers: make(map[string]func([]byte))}
}

func (f *fakeBackplane) Publish(userID string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, publishRec{userID, payload})
	return nil
}

type fakeSub struct {
	f      *fakeBackplane
	userID string
}

func (s fakeSub) Unsubscribe() error {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	s.f.unsubscribe++
	delete(s.f.handlers, s.userID)
	return nil
}

func (f *fakeBackplane) Subscribe(userID string, handler func([]byte)) (Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribes++
	f.handlers[userID] = handler
	return fakeSub{f: f, userID: userID}, nil
}

// trigger имитирует приход backplane-сообщения для userID.
func (f *fakeBackplane) trigger(userID string, payload []byte) {
	f.mu.Lock()
	h := f.handlers[userID]
	f.mu.Unlock()
	if h != nil {
		h(payload)
	}
}

func testHub(bp Backplane) *Hub {
	return NewHub(bp, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHub_SubscribeOncePerUser_RefCount(t *testing.T) {
	bp := newFakeBackplane()
	h := testHub(bp)
	c1, c2 := newConn(nil), newConn(nil)

	require.NoError(t, h.Add("u1", c1))
	require.NoError(t, h.Add("u1", c2))
	assert.Equal(t, 1, bp.subscribes, "одна подписка backplane на пользователя")
	assert.Equal(t, 2, h.LocalConnCount("u1"))

	h.Remove("u1", c1)
	assert.Equal(t, 0, bp.unsubscribe, "пока есть соединение — подписка жива")
	assert.Equal(t, 1, h.LocalConnCount("u1"))

	h.Remove("u1", c2)
	assert.Equal(t, 1, bp.unsubscribe, "последнее соединение снято — отписка")
	assert.Equal(t, 0, h.LocalConnCount("u1"))
}

func TestHub_DeliverToAllLocalConns(t *testing.T) {
	bp := newFakeBackplane()
	h := testHub(bp)
	c1, c2 := newConn(nil), newConn(nil)
	require.NoError(t, h.Add("u1", c1))
	require.NoError(t, h.Add("u1", c2))

	bp.trigger("u1", []byte("привет"))

	assert.Equal(t, "привет", string(<-c1.send))
	assert.Equal(t, "привет", string(<-c2.send))
}

func TestHub_DeliverUnknownUser_NoPanic(t *testing.T) {
	bp := newFakeBackplane()
	h := testHub(bp)
	// Доставка пользователю без соединений — просто игнорируется.
	h.deliver("ghost", []byte("x"))
}

func TestHub_Publish_ForwardsToBackplane(t *testing.T) {
	bp := newFakeBackplane()
	h := testHub(bp)

	require.NoError(t, h.Publish("recipient1", []byte("frame")))
	require.Len(t, bp.published, 1)
	assert.Equal(t, "recipient1", bp.published[0].userID)
	assert.Equal(t, "frame", string(bp.published[0].payload))
}
