// Package listingclient — HTTP-клиент внутреннего read-эндпоинта Listing
// (/internal/ads/{id}). По объявлению Reviews определяет продавца (владельца) —
// адресата отзыва (fetch-current-state, как модерация/чат).
package listingclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"bozor/pkg/shared/httpx"
	"bozor/services/reviews/internal/domain"
)

// Client — клиент внутреннего эндпоинта Listing.
type Client struct {
	baseURL string
	http    *http.Client
}

// New создаёт клиент Listing с таймаутом на запрос.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{baseURL: baseURL, http: httpx.NewClient(timeout)}
}

// adDTO — интересующая часть проекции объявления из /internal/ads/{id}.
type adDTO struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Status string `json:"status"`
}

// GetAd возвращает объявление по id. found=false, если объявления нет (404) —
// оставить отзыв нельзя.
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
		return domain.AdView{ID: dto.ID, UserID: dto.UserID, Status: dto.Status}, true, nil
	case http.StatusNotFound:
		return domain.AdView{}, false, nil
	default:
		return domain.AdView{}, false, fmt.Errorf("listingclient: get %q status %d: %s", id, resp.StatusCode, body)
	}
}
