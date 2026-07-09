package ws

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/chat/internal/app"
	"bozor/services/chat/internal/domain"
)

func TestToken_QueryThenHeader(t *testing.T) {
	q := httptest.NewRequest("GET", "/ws?token=abc", nil)
	assert.Equal(t, "abc", token(q), "токен из query")

	h := httptest.NewRequest("GET", "/ws", nil)
	h.Header.Set("Authorization", "Bearer xyz")
	assert.Equal(t, "xyz", token(h), "токен из Authorization")

	none := httptest.NewRequest("GET", "/ws", nil)
	assert.Empty(t, token(none))
}

func TestClassify(t *testing.T) {
	cases := map[error]string{
		app.ErrConversationNotFound: "conversation_not_found",
		app.ErrNotParticipant:       "forbidden",
		domain.ErrEmptyBody:         "empty_body",
		domain.ErrBodyTooLong:       "body_too_long",
	}
	for err, code := range cases {
		got, _ := classify(err)
		assert.Equal(t, code, got)
	}
}

func TestMessageFrame_Shape(t *testing.T) {
	m := domain.Message{ID: "m1", ConversationID: "c1", SenderID: "s1", Body: "привет",
		CreatedAt: time.Unix(1700000000, 0).UTC()}
	var out map[string]string
	require.NoError(t, json.Unmarshal(messageFrame(m), &out))
	assert.Equal(t, "message", out["type"])
	assert.Equal(t, "m1", out["id"])
	assert.Equal(t, "s1", out["sender_id"])
	assert.Equal(t, "привет", out["body"])

	var ack map[string]string
	require.NoError(t, json.Unmarshal(ackFrame(m), &ack))
	assert.Equal(t, "ack", ack["type"])
	assert.Equal(t, "m1", ack["id"])

	var e map[string]string
	require.NoError(t, json.Unmarshal(errorFrame("empty_body", "пусто"), &e))
	assert.Equal(t, "error", e["type"])
	assert.Equal(t, "empty_body", e["code"])
}
