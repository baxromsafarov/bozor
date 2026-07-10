// Package prefs — клиент внутреннего эндпоинта User/Profile для чтения
// эффективных настроек уведомлений получателя (fetch-current-state) с коротким
// кешем по TTL, чтобы не бить в User/Profile на каждое событие при всплесках.
package prefs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"bozor/pkg/shared/httpx"
	"bozor/services/notification/internal/domain"
)

// Client — кеширующий клиент настроек уведомлений.
type Client struct {
	baseURL string
	http    *http.Client
	ttl     time.Duration

	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	groups map[string]bool // event_type (группа) → enabled, только канал telegram
	exp    time.Time
}

// New создаёт клиент с таймаутом запроса и TTL кеша.
func New(baseURL string, timeout, ttl time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    httpx.NewClient(timeout),
		ttl:     ttl,
		cache:   make(map[string]entry),
	}
}

type prefsResponse struct {
	Prefs []struct {
		Channel   string `json:"channel"`
		EventType string `json:"event_type"`
		Enabled   bool   `json:"enabled"`
	} `json:"prefs"`
}

// Enabled сообщает, включена ли у пользователя группа уведомлений group на
// канале Telegram. Семантика «отсутствие = включено»: если группа не найдена в
// ответе, считается включённой. Пустая группа (GroupNone) всегда включена.
func (c *Client) Enabled(ctx context.Context, userID, group string) (bool, error) {
	if group == domain.GroupNone {
		return true, nil
	}
	groups, err := c.load(ctx, userID)
	if err != nil {
		return false, err
	}
	if v, ok := groups[group]; ok {
		return v, nil
	}
	return true, nil // absence = enabled
}

// load возвращает карту групп из кеша либо тянет её из User/Profile.
func (c *Client) load(ctx context.Context, userID string) (map[string]bool, error) {
	now := time.Now()

	c.mu.Lock()
	if e, ok := c.cache[userID]; ok && now.Before(e.exp) {
		c.mu.Unlock()
		return e.groups, nil
	}
	c.mu.Unlock()

	groups, err := c.fetch(ctx, userID)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[userID] = entry{groups: groups, exp: now.Add(c.ttl)}
	c.mu.Unlock()
	return groups, nil
}

// fetch запрашивает эффективные настройки уведомлений у User/Profile.
func (c *Client) fetch(ctx context.Context, userID string) (map[string]bool, error) {
	url := c.baseURL + "/internal/users/" + userID + "/notification-prefs"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("prefs: сборка запроса: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prefs: запрос настроек: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prefs: неожиданный статус %d для %s", resp.StatusCode, userID)
	}

	var body prefsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("prefs: разбор ответа: %w", err)
	}

	groups := make(map[string]bool, len(body.Prefs))
	for _, p := range body.Prefs {
		if p.Channel == domain.ChannelTelegram {
			groups[p.EventType] = p.Enabled
		}
	}
	return groups, nil
}
