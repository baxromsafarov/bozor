package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/notification/internal/telegram"
)

func TestSend_DisabledWhenNoToken(t *testing.T) {
	c := telegram.New("", "http://unused", time.Second)
	err := c.Send(context.Background(), 123, "hi")
	assert.ErrorIs(t, err, telegram.ErrDisabled)
}

func TestSend_Success(t *testing.T) {
	var gotChatID float64
	var gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotChatID, _ = body["chat_id"].(float64)
		gotText, _ = body["text"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	c := telegram.New("token", srv.URL, time.Second)
	require.NoError(t, c.Send(context.Background(), 42, "привет"))
	assert.EqualValues(t, 42, gotChatID)
	assert.Equal(t, "привет", gotText)
}

func TestSend_RateLimited_Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`))
	}))
	defer srv.Close()

	c := telegram.New("token", srv.URL, time.Second)
	err := c.Send(context.Background(), 1, "x")

	var se *telegram.SendError
	require.True(t, errors.As(err, &se))
	assert.True(t, se.Retryable)
	assert.Equal(t, http.StatusTooManyRequests, se.Code)
	assert.Equal(t, 7*time.Second, se.RetryAfter)
}

func TestSend_ServerError_Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	defer srv.Close()

	c := telegram.New("token", srv.URL, time.Second)
	var se *telegram.SendError
	require.True(t, errors.As(c.Send(context.Background(), 1, "x"), &se))
	assert.True(t, se.Retryable)
}

func TestSend_Forbidden_Permanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
	}))
	defer srv.Close()

	c := telegram.New("token", srv.URL, time.Second)
	var se *telegram.SendError
	require.True(t, errors.As(c.Send(context.Background(), 1, "x"), &se))
	assert.False(t, se.Retryable)
	assert.Contains(t, se.Description, "blocked")
}

func TestSend_NetworkError_Retryable(t *testing.T) {
	// Сервер закрыт сразу — соединение не установится.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := telegram.New("token", url, 200*time.Millisecond)
	var se *telegram.SendError
	require.True(t, errors.As(c.Send(context.Background(), 1, "x"), &se))
	assert.True(t, se.Retryable)
}
