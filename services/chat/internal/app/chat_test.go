package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/chat/internal/app"
	"bozor/services/chat/internal/domain"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// --- фейки ---

type fakeStore struct {
	ensured    domain.Conversation
	conv       domain.Conversation
	found      bool
	insertedEv events.Envelope
	insertMsg  domain.Message
	getErr     error
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

func (f *fakeStore) InsertMessageWithEvent(_ context.Context, msg domain.Message, ev events.Envelope) error {
	f.insertMsg = msg
	f.insertedEv = ev
	return nil
}

type fakeAds struct {
	ad    domain.AdView
	found bool
	err   error
}

func (f fakeAds) GetAd(_ context.Context, _ string) (domain.AdView, bool, error) {
	return f.ad, f.found, f.err
}

// --- StartConversation ---

func TestStartConversation_NewWithSellerFromListing(t *testing.T) {
	store := &fakeStore{}
	ads := fakeAds{ad: domain.AdView{ID: "ad1", UserID: "seller1"}, found: true}
	svc := app.NewService(store, ads, discardLog())

	conv, err := svc.StartConversation(context.Background(), "ad1", "buyer1")
	require.NoError(t, err)
	assert.Equal(t, "ad1", conv.AdID)
	assert.Equal(t, "buyer1", conv.BuyerID)
	assert.Equal(t, "seller1", conv.SellerID, "продавец — владелец объявления из Listing")
	assert.NotEmpty(t, store.ensured.ID, "диалог сохранён")
}

func TestStartConversation_AdNotFound(t *testing.T) {
	svc := app.NewService(&fakeStore{}, fakeAds{found: false}, discardLog())
	_, err := svc.StartConversation(context.Background(), "ad1", "buyer1")
	assert.ErrorIs(t, err, app.ErrAdNotFound)
}

func TestStartConversation_SelfChatRejected(t *testing.T) {
	ads := fakeAds{ad: domain.AdView{ID: "ad1", UserID: "owner"}, found: true}
	svc := app.NewService(&fakeStore{}, ads, discardLog())
	_, err := svc.StartConversation(context.Background(), "ad1", "owner")
	assert.ErrorIs(t, err, app.ErrSelfConversation, "владелец не может писать сам себе")
}

// --- SendMessage ---

func TestSendMessage_PublishesEventToCounterpart(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", AdID: "ad1", BuyerID: "buyer1", SellerID: "seller1"}, found: true}
	svc := app.NewService(store, fakeAds{}, discardLog())

	msg, err := svc.SendMessage(context.Background(), "c1", "buyer1", "  привет  ")
	require.NoError(t, err)
	assert.Equal(t, "привет", msg.Body, "тело обрезано")

	assert.Equal(t, events.SubjectChatMessageSent, store.insertedEv.Type)
	var pl struct {
		UserID    string `json:"user_id"`
		SenderID  string `json:"sender_id"`
		MessageID string `json:"message_id"`
	}
	require.NoError(t, store.insertedEv.Decode(&pl))
	assert.Equal(t, "seller1", pl.UserID, "адресат события — второй участник")
	assert.Equal(t, "buyer1", pl.SenderID)
	assert.Equal(t, msg.ID, pl.MessageID)
}

func TestSendMessage_NotParticipant(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", BuyerID: "buyer1", SellerID: "seller1"}, found: true}
	svc := app.NewService(store, fakeAds{}, discardLog())
	_, err := svc.SendMessage(context.Background(), "c1", "stranger", "привет")
	assert.ErrorIs(t, err, app.ErrNotParticipant)
}

func TestSendMessage_ConversationNotFound(t *testing.T) {
	svc := app.NewService(&fakeStore{found: false}, fakeAds{}, discardLog())
	_, err := svc.SendMessage(context.Background(), "c1", "buyer1", "привет")
	assert.ErrorIs(t, err, app.ErrConversationNotFound)
}

func TestSendMessage_EmptyBodyRejected(t *testing.T) {
	svc := app.NewService(&fakeStore{}, fakeAds{}, discardLog())
	_, err := svc.SendMessage(context.Background(), "c1", "buyer1", "   ")
	assert.ErrorIs(t, err, domain.ErrEmptyBody, "пустое тело не читает диалог")
}

// --- ListMessages ---

func TestListMessages_ParticipantOnly(t *testing.T) {
	store := &fakeStore{conv: domain.Conversation{ID: "c1", BuyerID: "buyer1", SellerID: "seller1"}, found: true}
	svc := app.NewService(store, fakeAds{}, discardLog())

	msgs, err := svc.ListMessages(context.Background(), "c1", "buyer1", 30)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)

	_, err = svc.ListMessages(context.Background(), "c1", "stranger", 30)
	assert.ErrorIs(t, err, app.ErrNotParticipant)
}

func TestListMessages_PropagatesStoreError(t *testing.T) {
	store := &fakeStore{getErr: errors.New("db down")}
	svc := app.NewService(store, fakeAds{}, discardLog())
	_, err := svc.ListMessages(context.Background(), "c1", "buyer1", 30)
	assert.Error(t, err)
}
