// Package listingclient — HTTP-клиент внутренних read-эндпоинтов Listing
// (/internal/ads/*). Индексатор Search читает актуальное состояние объявления
// как источник истины и синхронизирует read-модель Typesense (Stage 4.2).
package listingclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"bozor/pkg/shared/httpx"
)

// Attr — значение атрибута объявления (slug из Catalog).
type Attr struct {
	Slug  string `json:"slug"`
	Value string `json:"value"`
}

// Ad — полная проекция объявления из внутреннего экспорта Listing.
type Ad struct {
	ID          string   `json:"id"`
	UserID      string   `json:"user_id"`
	CategoryID  string   `json:"category_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Price       int64    `json:"price"`
	Currency    string   `json:"currency"`
	RegionID    int32    `json:"region_id"`
	CityID      *int64   `json:"city_id"`
	Lat         *float64 `json:"lat"`
	Lng         *float64 `json:"lng"`
	Status      string   `json:"status"`
	Attributes  []Attr   `json:"attributes"`
	CreatedAt   string   `json:"created_at"`
	BumpedAt    string   `json:"bumped_at"`
	// Промо-состояние для топ-блока Search (Stage 8.6).
	IsTop         bool   `json:"is_top"`
	PromotionRank int32  `json:"promotion_rank"`
	PromoEndsAt   string `json:"promo_ends_at"`
}

type exportListResponse struct {
	Ads       []Ad   `json:"ads"`
	NextAfter string `json:"next_after"`
}

// Client — клиент внутренних эндпоинтов Listing.
type Client struct {
	baseURL string
	http    *http.Client
}

// New создаёт клиент Listing с таймаутом на запрос.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{baseURL: baseURL, http: httpx.NewClient(timeout)}
}

// GetAd возвращает объявление по id. found=false, если объявления нет (404 —
// удалено): индексатору это сигнал снять документ из индекса.
func (c *Client) GetAd(ctx context.Context, id string) (Ad, bool, error) {
	status, body, err := c.get(ctx, "/internal/ads/"+id)
	if err != nil {
		return Ad{}, false, err
	}
	switch status {
	case http.StatusOK:
		var ad Ad
		if err := json.Unmarshal(body, &ad); err != nil {
			return Ad{}, false, fmt.Errorf("listingclient: разбор объявления: %w", err)
		}
		return ad, true, nil
	case http.StatusNotFound:
		return Ad{}, false, nil
	default:
		return Ad{}, false, fmt.Errorf("listingclient: get %q status %d: %s", id, status, body)
	}
}

// ListActive возвращает страницу активных объявлений (keyset по id) и курсор
// следующей страницы (пусто — конец).
func (c *Client) ListActive(ctx context.Context, after string, limit int) ([]Ad, string, error) {
	q := url.Values{}
	if after != "" {
		q.Set("after", after)
	}
	q.Set("limit", strconv.Itoa(limit))
	status, body, err := c.get(ctx, "/internal/ads/export?"+q.Encode())
	if err != nil {
		return nil, "", err
	}
	if status != http.StatusOK {
		return nil, "", fmt.Errorf("listingclient: export status %d: %s", status, body)
	}
	var resp exportListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("listingclient: разбор экспорта: %w", err)
	}
	return resp.Ads, resp.NextAfter, nil
}

// get выполняет GET-запрос и возвращает статус и тело.
func (c *Client) get(ctx context.Context, path string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("listingclient: сборка запроса: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("listingclient: запрос %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("listingclient: чтение ответа: %w", err)
	}
	return resp.StatusCode, body, nil
}
