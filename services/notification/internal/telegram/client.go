// Package telegram — тонкий клиент Telegram Bot API для доставки уведомлений.
// Отдаёт классифицированные ошибки: постоянные (не повторять — заблокирован
// ботом, некорректный chat_id) и повторяемые (429/5xx/сеть — повторить позже).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrDisabled возвращается, когда токен бота не задан: канал отключён
// (доставка помечается skipped). Удобно для локальной разработки без бота.
var ErrDisabled = errors.New("telegram: канал отключён (пустой токен)")

// SendError — ошибка вызова Bot API с признаком повторяемости.
type SendError struct {
	Code        int           // HTTP-код (или 0 для сетевой ошибки)
	Description string        // описание из ответа Bot API
	RetryAfter  time.Duration // рекомендованная пауза (из parameters.retry_after)
	Retryable   bool          // true => повторить доставку; false => постоянная ошибка
}

func (e *SendError) Error() string {
	return fmt.Sprintf("telegram: sendMessage код=%d retryable=%t: %s", e.Code, e.Retryable, e.Description)
}

// Client — клиент Bot API (метод sendMessage).
type Client struct {
	token string
	base  string
	http  *http.Client
}

// New создаёт клиент. Пустой token => канал отключён (Send возвращает ErrDisabled).
func New(token, base string, timeout time.Duration) *Client {
	return &Client{
		token: token,
		base:  base,
		http:  &http.Client{Timeout: timeout},
	}
}

// apiResponse — общий конверт ответа Bot API.
type apiResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// Send отправляет текстовое сообщение в чат chatID.
//   - nil            — доставлено;
//   - ErrDisabled    — канал отключён (нет токена);
//   - *SendError     — ошибка Bot API (см. Retryable).
func (c *Client) Send(ctx context.Context, chatID int64, text string) error {
	if c.token == "" {
		return ErrDisabled
	}

	body, err := json.Marshal(map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
	if err != nil {
		return &SendError{Description: "сериализация запроса: " + err.Error(), Retryable: false}
	}

	url := c.base + "/bot" + c.token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return &SendError{Description: "формирование запроса: " + err.Error(), Retryable: false}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// Сетевая ошибка/таймаут — повторяемо.
		return &SendError{Code: 0, Description: err.Error(), Retryable: true}
	}
	defer func() { _ = resp.Body.Close() }()

	var ar apiResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar) // тело может быть пустым при сетевых прокси

	if resp.StatusCode == http.StatusOK && ar.OK {
		return nil
	}
	return classify(resp.StatusCode, ar)
}

// classify превращает неуспешный ответ в *SendError с признаком повторяемости.
// Повторяемо: 429 (rate limit) и 5xx (временная недоступность Bot API).
// Постоянно: 400/401/403/404 (заблокирован ботом, неверный chat_id и т.п.).
func classify(status int, ar apiResponse) *SendError {
	se := &SendError{Code: status, Description: ar.Description}
	switch {
	case status == http.StatusTooManyRequests:
		se.Retryable = true
		se.RetryAfter = time.Duration(ar.Parameters.RetryAfter) * time.Second
	case status >= 500:
		se.Retryable = true
	default:
		se.Retryable = false
	}
	if se.Description == "" {
		se.Description = http.StatusText(status)
	}
	return se
}
