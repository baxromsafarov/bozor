// Package telegram — тонкий клиент Telegram Bot API (только нужные методы).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.telegram.org"

// Client — клиент Telegram Bot API. При пустом токене отправка отключена
// (no-op) — удобно для локальной разработки без реального бота.
type Client struct {
	token string
	base  string
	http  *http.Client
}

// New создаёт клиент с токеном бота.
func New(token string) *Client {
	return &Client{
		token: token,
		base:  defaultBaseURL,
		http:  &http.Client{Timeout: 10 * time.Second},
	}
}

// WithBaseURL переопределяет базовый URL (для тестов).
func (c *Client) WithBaseURL(base string) *Client {
	c.base = base
	return c
}

// SendContactRequest отправляет сообщение text с reply-кнопкой запроса
// контакта (подпись кнопки — buttonText).
func (c *Client) SendContactRequest(ctx context.Context, chatID int64, text, buttonText string) error {
	return c.sendMessage(ctx, chatID, text, contactKeyboard(buttonText))
}

// SendText отправляет текстовое сообщение и убирает reply-клавиатуру.
func (c *Client) SendText(ctx context.Context, chatID int64, text string) error {
	return c.sendMessage(ctx, chatID, text, removeKeyboard())
}

func (c *Client) sendMessage(ctx context.Context, chatID int64, text string, markup any) error {
	if c.token == "" {
		return nil // бот не сконфигурирован — отправка отключена
	}

	body, err := json.Marshal(map[string]any{
		"chat_id":      chatID,
		"text":         text,
		"reply_markup": markup,
	})
	if err != nil {
		return fmt.Errorf("telegram: сериализация запроса: %w", err)
	}

	url := c.base + "/bot" + c.token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: формирование запроса: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: отправка sendMessage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: sendMessage вернул статус %d", resp.StatusCode)
	}
	return nil
}

// contactKeyboard — reply-клавиатура с кнопкой request_contact.
func contactKeyboard(buttonText string) map[string]any {
	return map[string]any{
		"keyboard": [][]map[string]any{{{
			"text":            "📱 " + buttonText,
			"request_contact": true,
		}}},
		"resize_keyboard":   true,
		"one_time_keyboard": true,
	}
}

// removeKeyboard убирает reply-клавиатуру после успешного шага.
func removeKeyboard() map[string]any {
	return map[string]any{"remove_keyboard": true}
}
