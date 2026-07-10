// Package reviewsclient — HTTP-клиент внутреннего эндпоинта Reviews
// (/internal/users/{id}/rating). Агрегатор рейтинга Profile по bozor.review.created
// перечитывает актуальный агрегат продавца из Reviews (fetch-current-state,
// ADR-024): снятые модератором отзывы автоматически выпадают из агрегата.
package reviewsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"bozor/services/user-profile/internal/domain"
)

// Client — клиент внутреннего эндпоинта Reviews.
type Client struct {
	baseURL string
	http    *http.Client
}

// New создаёт клиент Reviews с таймаутом на запрос.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

// ratingDTO — проекция агрегата рейтинга из /internal/users/{id}/rating.
type ratingDTO struct {
	AvgRating    float64 `json:"avg_rating"`
	ReviewsCount int     `json:"reviews_count"`
}

// GetRating возвращает агрегат рейтинга продавца (средняя оценка и количество)
// по активным отзывам. Отсутствие отзывов → нулевой рейтинг (0/0).
func (c *Client) GetRating(ctx context.Context, userID string) (domain.Rating, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/users/"+userID+"/rating", nil)
	if err != nil {
		return domain.Rating{}, fmt.Errorf("reviewsclient: сборка запроса: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.Rating{}, fmt.Errorf("reviewsclient: запрос рейтинга: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.Rating{}, fmt.Errorf("reviewsclient: чтение ответа: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Rating{}, fmt.Errorf("reviewsclient: рейтинг %q status %d: %s", userID, resp.StatusCode, body)
	}

	var dto ratingDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return domain.Rating{}, fmt.Errorf("reviewsclient: разбор рейтинга: %w", err)
	}
	return domain.Rating{AvgRating: dto.AvgRating, ReviewsCount: dto.ReviewsCount}, nil
}
