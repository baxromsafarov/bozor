package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/chat/internal/app"
	"bozor/services/chat/internal/domain"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// --- фейки ---

type fakeStore struct {
	ensured      domain.Conversation
	conv         domain.Conversation
	found        bool
	insertedEv   *events.Envelope
	insertMsg    domain.Message
	getErr       error
	markN        int64
	markedReader string
}

func (f *fakeStore) EnsureConversation(_ context.Context, conv domain.Conversation) (domain.Conversation, error) {
	f.ensured = conv
	return conv, nil
}

func (f *fakeStore) GetConversation(_ context.Context, _ string) (domain.Conversation, bool, error) {
	if f.getErr != nil {
		return domain.Conversation{}, false, f.getErr
	}
	return f.conv, f.found, nil
}

func (f *fakeStore) ListConversations(_ context.Context, _ string, _ int) ([]domain.Conversation, error) {
	return []domain.Conversation{f.conv}, nil
}

func (f *fakeStore) ListMessages(_ context.Context, _ string, _ int) ([]domain.Message, error) {
	return []domain.Message{{ID: "m1", Body: "hi"}}, nil
}

func (f *fakeStore) InsertMessage(_ context.Context, msg domain.Message, ev *events.Envelope) error {
	f.insertMsg = msg
	f.insertedEv = ev
	return nil
}

func (f *fakeStore) MarkRead(_ context.Context, _, readerID string, _ time.Time) (int64, error) {
	f.markedReader = readerID
	return f.markN, nil
}

type fakeAds struct {
	ad    domain.AdView
	found bool
	err   error
}

func (f fakeAds) GetAd(_ context.Context, _ string) (domain.AdView, bool, error) {
	return f.ad, f.found, f.err
}

// fakePresence управляет ответом Online.
type fakePresence struct {
	online bool
	err    error
}

func (f fakePresence) Online(context.Context, string) (bool, error) { return f.online, f.err }

// fakeRT фиксирует realtime-доставку и уведомления о прочтении.
type fakeRT struct {
	deliveredTo  string
	deliveredMsg domain.Message
	readTo       string
	readReceipt  domain.ReadReceipt
}

func (f *fakeRT) DeliverMessage(recipientID string, msg domain.Message) {
	f.deliveredTo, f.deliveredMsg = recipientID, msg
}
func (f *fakeRT) NotifyRead(recipientID string, r domain.ReadReceipt) {
	f.readTo, f.readReceipt = recipientID, r
}

func newSvc(store *fakeStore, ads fakeAds, presence app.Presence, rt app.Realtime) *app.Service {
	return app.NewService(store, ads, presence, rt, discardLog())
}

// --- StartConversation ---

func TestStartConversation_NewWithSellerFromListing(t *testing.T) {
	store := &fakeStore{}
	ads := fakeAds{ad: domain.AdView{ID: "ad1", UserID: "seller1"}, found: true}
	svc := newSvc(store, ads, fakePresence{}, &fakeRT{})

	conv, err := svc.StartConversation(context.Background(), "ad1", "buyer1")
	require.NoError(t, err)
	assert.Equal(t, "ad1", conv.AdID)
	assert.Equal(t, "buyer1", conv.BuyerID)
	assert.Equal(t, "seller1", conv.SellerID, "продавец — владелец объявления из Listing")
	assert.NotEmpty(t, store.ensured.ID, "диалог сохранён")
}

func TestStartConversation_AdNotFound(t *testing.T) {
	svc := newSvc(&fakeStore{}, fakeAds{found: false}, fakePresence{}, &fakeRT{})
	_, err := svc.StartConversation(context.Background(), "ad1", "buyer1")
	assert.ErrorIs(t, err, app.ErrAdNotFound)
}

func TestStartConversation_SelfChatRejected(t *testing.T) {
	ads := fakeAds{ad: domain.AdView{ID: "ad1", UserID: "owner"}, found: true}
	svc := newSvc(&fakeStore{}, ads, fakePresence{}, &fakeRT{})
	_, err := svc.StartConversation(context.Background(), "ad1", "owner")
	assert.ErrorIs(t, err, app.ErrSelfConversation, "владелец не может писать сам себе")
}

// --- SendMessage: онлайн-гейтинг ---

func TestSendMessage_RecipientOffline_PublishesEventAndDelivers(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", AdID: "ad1", BuyerID: "buyer1", SellerID: "seller1"}, found: true}
	rt := &fakeRT{}
	svc := newSvc(store, fakeAds{}, fakePresence{online: false}, rt)

	msg, recipient, err := svc.SendMessage(context.Background(), "c1", "buyer1", "  привет  ")
	require.NoError(t, err)
	assert.Equal(t, "привет", msg.Body, "тело обрезано")
	assert.Equal(t, "seller1", recipient, "адресат — второй участник")

	require.NotNil(t, store.insertedEv, "оффлайн: событие Telegram публикуется")
	assert.Equal(t, events.SubjectChatMessageSent, store.insertedEv.Type)
	var pl struct {
		UserID    string `json:"user_id"`
		SenderID  string `json:"sender_id"`
		MessageID string `json:"message_id"`
	}
	require.NoError(t, store.insertedEv.Decode(&pl))
	assert.Equal(t, "seller1", pl.UserID)
	assert.Equal(t, "buyer1", pl.SenderID)
	assert.Equal(t, msg.ID, pl.MessageID)

	assert.Equal(t, "seller1", rt.deliveredTo, "realtime-доставка выполняется всегда")
}

func TestSendMessage_RecipientOnline_NoEventButDelivers(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", BuyerID: "buyer1", SellerID: "seller1"}, found: true}
	rt := &fakeRT{}
	svc := newSvc(store, fakeAds{}, fakePresence{online: true}, rt)

	msg, _, err := svc.SendMessage(context.Background(), "c1", "buyer1", "привет")
	require.NoError(t, err)
	assert.Nil(t, store.insertedEv, "онлайн: Telegram-событие НЕ публикуется")
	assert.Equal(t, "seller1", rt.deliveredTo, "доставка по WS")
	assert.Equal(t, msg.ID, rt.deliveredMsg.ID)
}

func TestSendMessage_PresenceError_TreatedAsOffline(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", BuyerID: "buyer1", SellerID: "seller1"}, found: true}
	svc := newSvc(store, fakeAds{}, fakePresence{err: errors.New("nats down")}, &fakeRT{})
	_, _, err := svc.SendMessage(context.Background(), "c1", "buyer1", "привет")
	require.NoError(t, err)
	assert.NotNil(t, store.insertedEv, "ошибка присутствия → оффлайн → событие публикуется")
}

func TestSendMessage_NotParticipant(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", BuyerID: "buyer1", SellerID: "seller1"}, found: true}
	svc := newSvc(store, fakeAds{}, fakePresence{}, &fakeRT{})
	_, _, err := svc.SendMessage(context.Background(), "c1", "stranger", "привет")
	assert.ErrorIs(t, err, app.ErrNotParticipant)
}

func TestSendMessage_ConversationNotFound(t *testing.T) {
	svc := newSvc(&fakeStore{found: false}, fakeAds{}, fakePresence{}, &fakeRT{})
	_, _, err := svc.SendMessage(context.Background(), "c1", "buyer1", "привет")
	assert.ErrorIs(t, err, app.ErrConversationNotFound)
}

func TestSendMessage_EmptyBodyRejected(t *testing.T) {
	svc := newSvc(&fakeStore{}, fakeAds{}, fakePresence{}, &fakeRT{})
	_, _, err := svc.SendMessage(context.Background(), "c1", "buyer1", "   ")
	assert.ErrorIs(t, err, domain.ErrEmptyBody, "пустое тело не читает диалог")
}

// --- MarkRead ---

func TestMarkRead_NotifiesCounterpart(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", BuyerID: "buyer1", SellerID: "seller1"}, found: true, markN: 3}
	rt := &fakeRT{}
	svc := newSvc(store, fakeAds{}, fakePresence{}, rt)

	n, err := svc.MarkRead(context.Background(), "c1", "buyer1")
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, "buyer1", store.markedReader)
	assert.Equal(t, "seller1", rt.readTo, "уведомляется автор прочитанных сообщений")
	assert.Equal(t, "buyer1", rt.readReceipt.ReaderID)
}

func TestMarkRead_NothingUnread_NoNotify(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", BuyerID: "buyer1", SellerID: "seller1"}, found: true, markN: 0}
	rt := &fakeRT{}
	svc := newSvc(store, fakeAds{}, fakePresence{}, rt)

	n, err := svc.MarkRead(context.Background(), "c1", "buyer1")
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Empty(t, rt.readTo, "нечего отмечать — не уведомляем")
}

func TestMarkRead_NotParticipant(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", BuyerID: "buyer1", SellerID: "seller1"}, found: true}
	svc := newSvc(store, fakeAds{}, fakePresence{}, &fakeRT{})
	_, err := svc.MarkRead(context.Background(), "c1", "stranger")
	assert.ErrorIs(t, err, app.ErrNotParticipant)
}

// --- ListMessages ---

func TestListMessages_ParticipantOnly(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", BuyerID: "buyer1", SellerID: "seller1"}, found: true}
	svc := newSvc(store, fakeAds{}, fakePresence{}, &fakeRT{})

	msgs, err := svc.ListMessages(context.Background(), "c1", "buyer1", 30)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)

	_, err = svc.ListMessages(context.Background(), "c1", "stranger", 30)
	assert.ErrorIs(t, err, app.ErrNotParticipant)
}

func TestListMessages_PropagatesStoreError(t *testing.T) {
	store := &fakeStore{getErr: errors.New("db down")}
	svc := newSvc(store, fakeAds{}, fakePresence{}, &fakeRT{})
	_, err := svc.ListMessages(context.Background(), "c1", "buyer1", 30)
	assert.Error(t, err)
}
