// Package listingclient — HTTP-клиент внутреннего read-эндпоинта Listing
// (/internal/ads/{id}). При покупке услуги payments читает объявление как
// источник истины: владельца (проверка прав), регион/категорию (для цены),
// заголовок (для события) и статус (продвигать можно только активное).
package listingclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"bozor/services/payments/internal/domain"
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
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	CategoryID string `json:"category_id"`
	RegionID   int    `json:"region_id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
}

// GetAd возвращает объявление по id. found=false, если объявления нет (404).
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
			RegionID: dto.RegionID, Title: dto.Title, Status: dto.Status,
		}, true, nil
	case http.StatusNotFound:
		return domain.AdView{}, false, nil
	default:
		return domain.AdView{}, false, fmt.Errorf("listingclient: get %q status %d: %s", id, resp.StatusCode, body)
	}
}

// Bump поднимает объявление в ленте через внутренний эндпоинт Listing
// (POST /internal/ads/{id}/bump): Listing выставляет bumped_at и публикует
// bozor.ad.bumped. Возвращает bumped=true при 200; false при 404 (объявления нет
// или оно не активно — поднимать нечего, день считается исполненным). На прочих
// статусах и сетевых сбоях — ошибка (день будет освобождён для повторной попытки).
func (c *Client) Bump(ctx context.Context, id string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/ads/"+id+"/bump", nil)
	if err != nil {
		return false, fmt.Errorf("listingclient: сборка запроса поднятия: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("listingclient: запрос поднятия: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("listingclient: bump %q status %d: %s", id, resp.StatusCode, body)
	}
}
