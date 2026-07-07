package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_NoOpWithoutToken(t *testing.T) {
	// Без токена отправка не выполняется и не ошибается.
	c := New("")
	require.NoError(t, c.SendText(context.Background(), 1, "hi"))
	require.NoError(t, c.SendContactRequest(context.Background(), 1, "hi", "phone"))
}

func TestClient_SendContactRequest(t *testing.T) {
	var gotPath string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := New("TESTTOKEN").WithBaseURL(srv.URL)
	require.NoError(t, c.SendContactRequest(context.Background(), 555, "Отправьте номер", "Отправить номер"))

	assert.Equal(t, "/botTESTTOKEN/sendMessage", gotPath)
	assert.Equal(t, float64(555), payload["chat_id"])
	assert.Equal(t, "Отправьте номер", payload["text"])

	markup, ok := payload["reply_markup"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, markup["one_time_keyboard"])
	// В клавиатуре есть кнопка с request_contact.
	kb := markup["keyboard"].([]any)
	row := kb[0].([]any)
	btn := row[0].(map[string]any)
	assert.Equal(t, true, btn["request_contact"])
}

func TestClient_SendTextRemovesKeyboard(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New("T").WithBaseURL(srv.URL)
	require.NoError(t, c.SendText(context.Background(), 7, "✅"))

	markup := payload["reply_markup"].(map[string]any)
	assert.Equal(t, true, markup["remove_keyboard"])
}

func TestClient_ErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New("T").WithBaseURL(srv.URL)
	err := c.SendText(context.Background(), 1, "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "статус 400")
}
