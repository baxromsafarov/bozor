package transport

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/chat/internal/app"
	"bozor/services/chat/internal/domain"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeChat struct {
	startErr, sendErr, msgErr, readErr error
	gotAdID, gotBuyer                  string
	gotConvID, gotSender               string
	gotBody                            string
	gotReadConv, gotReader             string
	markN                              int
}

func (f *fakeChat) StartConversation(_ context.Context, adID, buyerID string) (domain.Conversation, error) {
	f.gotAdID, f.gotBuyer = adID, buyerID
	if f.startErr != nil {
		return domain.Conversation{}, f.startErr
	}
	return domain.Conversation{ID: "c1", AdID: adID, BuyerID: buyerID, SellerID: "seller1",
		CreatedAt: time.Unix(1, 0).UTC(), LastMessageAt: time.Unix(1, 0).UTC()}, nil
}

func (f *fakeChat) SendMessage(_ context.Context, convID, senderID, body string) (domain.Message, string, error) {
	f.gotConvID, f.gotSender, f.gotBody = convID, senderID, body
	if f.sendErr != nil {
		return domain.Message{}, "", f.sendErr
	}
	return domain.Message{ID: "m1", ConversationID: convID, SenderID: senderID, Body: body, CreatedAt: time.Unix(2, 0).UTC()}, "seller1", nil
}

func (f *fakeChat) ListConversations(context.Context, string, int) ([]domain.Conversation, error) {
	return []domain.Conversation{{ID: "c1", AdID: "ad1", BuyerID: "b", SellerID: "s",
		CreatedAt: time.Unix(1, 0).UTC(), LastMessageAt: time.Unix(3, 0).UTC()}}, nil
}

func (f *fakeChat) ListMessages(_ context.Context, _, _ string, _ int) ([]domain.Message, error) {
	if f.msgErr != nil {
		return nil, f.msgErr
	}
	return []domain.Message{{ID: "m1", SenderID: "b", Body: "hi", CreatedAt: time.Unix(2, 0).UTC()}}, nil
}

func (f *fakeChat) MarkRead(_ context.Context, convID, readerID string) (int, error) {
	f.gotReadConv, f.gotReader = convID, readerID
	return f.markN, f.readErr
}

// chatServer собирает роутер и проставляет проброшенные заголовки идентичности.
func chatServer(h *ConversationHandler, userID string) http.Handler {
	router := NewRouter(Deps{Log: discardLog(), Conversations: h})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if userID != "" {
			req.Header.Set("X-User-Id", userID)
		}
		router.ServeHTTP(w, req)
	})
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	h.ServeHTTP(rec, req)
	return rec
}

func TestStart_201(t *testing.T) {
	fc := &fakeChat{}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "buyer1"),
		http.MethodPost, "/api/v1/conversations", `{"ad_id":"ad1"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "ad1", fc.gotAdID)
	assert.Equal(t, "buyer1", fc.gotBuyer, "инициатор — текущий пользователь")
}

func TestStart_Anonymous_401(t *testing.T) {
	fc := &fakeChat{}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), ""),
		http.MethodPost, "/api/v1/conversations", `{"ad_id":"ad1"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestStart_MissingAdID_422(t *testing.T) {
	fc := &fakeChat{}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "buyer1"),
		http.MethodPost, "/api/v1/conversations", `{}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestStart_AdNotFound_404(t *testing.T) {
	fc := &fakeChat{startErr: app.ErrAdNotFound}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "buyer1"),
		http.MethodPost, "/api/v1/conversations", `{"ad_id":"ad1"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestStart_SelfChat_422(t *testing.T) {
	fc := &fakeChat{startErr: app.ErrSelfConversation}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "owner"),
		http.MethodPost, "/api/v1/conversations", `{"ad_id":"ad1"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestSend_201(t *testing.T) {
	fc := &fakeChat{}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "buyer1"),
		http.MethodPost, "/api/v1/conversations/c1/messages", `{"body":"привет"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "c1", fc.gotConvID)
	assert.Equal(t, "buyer1", fc.gotSender)
	assert.Equal(t, "привет", fc.gotBody)
}

func TestSend_NotParticipant_403(t *testing.T) {
	fc := &fakeChat{sendErr: app.ErrNotParticipant}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "stranger"),
		http.MethodPost, "/api/v1/conversations/c1/messages", `{"body":"привет"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestSend_EmptyBody_422(t *testing.T) {
	fc := &fakeChat{sendErr: domain.ErrEmptyBody}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "buyer1"),
		http.MethodPost, "/api/v1/conversations/c1/messages", `{"body":"   "}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestList_OK(t *testing.T) {
	fc := &fakeChat{}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "b"),
		http.MethodGet, "/api/v1/conversations", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"conversations"`)
	assert.Contains(t, rec.Body.String(), `"unread_count"`)
}

func TestRead_OK(t *testing.T) {
	fc := &fakeChat{markN: 4}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "buyer1"),
		http.MethodPost, "/api/v1/conversations/c1/read", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "c1", fc.gotReadConv)
	assert.Equal(t, "buyer1", fc.gotReader)
	assert.Contains(t, rec.Body.String(), `"marked":4`)
}

func TestRead_NotParticipant_403(t *testing.T) {
	fc := &fakeChat{readErr: app.ErrNotParticipant}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "stranger"),
		http.MethodPost, "/api/v1/conversations/c1/read", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestMessages_OK(t *testing.T) {
	fc := &fakeChat{}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "b"),
		http.MethodGet, "/api/v1/conversations/c1/messages", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"messages"`)
}

func TestMessages_NotParticipant_403(t *testing.T) {
	fc := &fakeChat{msgErr: app.ErrNotParticipant}
	rec := do(t, chatServer(NewConversationHandler(fc, discardLog()), "stranger"),
		http.MethodGet, "/api/v1/conversations/c1/messages", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
