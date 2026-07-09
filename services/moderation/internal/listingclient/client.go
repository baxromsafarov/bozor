// Package listingclient — HTTP-клиент внутреннего read-эндпоинта Listing
// (/internal/ads/{id}). Модератор читает объявление целиком (title/description/
// категория/статус) как источник истины для авто-проверок (событие — триггер).
package listingclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"bozor/services/moderation/internal/domain"
)

// Client — клиент внутреннего эндпоинта Listing.
type Client struct {
	baseURL string
	http    *http.Client
}

// New создаёт клиент Listing с таймаутом на запрос.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

// adDTO — интересующая часть проекции объявления из /internal/ads/{id}.
type adDTO struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	CategoryID  string `json:"category_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// GetAd возвращает объявление по id для авто-проверок. found=false, если
// объявления нет (404 — удалено до обработки события).
func (c *Client) GetAd(ctx context.Context, id string) (domain.AdView, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/ads/"+id, nil)
	if err != nil {
		return domain.AdView{}, false, fmt.Errorf("listingclient: сборка запроса: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.AdView{}, false, fmt.Errorf("listingclient: запрос объявления: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.AdView{}, false, fmt.Errorf("listingclient: чтение ответа: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var dto adDTO
		if err := json.Unmarshal(body, &dto); err != nil {
			return domain.AdView{}, false, fmt.Errorf("listingclient: разбор объявления: %w", err)
		}
		return domain.AdView{
			ID: dto.ID, UserID: dto.UserID, CategoryID: dto.CategoryID,
			Title: dto.Title, Description: dto.Description, Status: dto.Status,
		}, true, nil
	case http.StatusNotFound:
		return domain.AdView{}, false, nil
	default:
		return domain.AdView{}, false, fmt.Errorf("listingclient: get %q status %d: %s", id, resp.StatusCode, body)
	}
}
