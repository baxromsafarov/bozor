// Package listingclient — HTTP-клиент внутреннего read-эндпоинта Listing
// (/internal/ads/{id}). Matcher сохранённых поисков читает актуальное состояние
// одобренного объявления как источник истины для оценки фильтров (Stage 5.3).
package listingclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"bozor/services/favorites-savedsearch/internal/domain"
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

type attr struct {
	Slug  string `json:"slug"`
	Value string `json:"value"`
}

// adDTO — интересующая часть проекции объявления из /internal/ads/{id}.
type adDTO struct {
	ID          string `json:"id"`
	CategoryID  string `json:"category_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Price       int64  `json:"price"`
	Currency    string `json:"currency"`
	RegionID    int16  `json:"region_id"`
	CityID      *int64 `json:"city_id"`
	Attributes  []attr `json:"attributes"`
}

// GetAd возвращает объявление по id для оценки фильтров. found=false, если
// объявления нет (404 — удалено до обработки одобрения).
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
		return toAdView(dto), true, nil
	case http.StatusNotFound:
		return domain.AdView{}, false, nil
	default:
		return domain.AdView{}, false, fmt.Errorf("listingclient: get %q status %d: %s", id, resp.StatusCode, body)
	}
}

func toAdView(dto adDTO) domain.AdView {
	ad := domain.AdView{
		ID: dto.ID, CategoryID: dto.CategoryID, RegionID: dto.RegionID, CityID: dto.CityID,
		Price: dto.Price, Currency: dto.Currency, Title: dto.Title, Description: dto.Description,
	}
	if len(dto.Attributes) > 0 {
		ad.Attrs = make(map[string]string, len(dto.Attributes))
		for _, a := range dto.Attributes {
			ad.Attrs[a.Slug] = a.Value
		}
	}
	return ad
}
