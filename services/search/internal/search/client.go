// Package search — тонкий HTTP-клиент Typesense и определение коллекции ads
// (read-модель поиска, CQRS-lite). Без внешних клиентских зависимостей —
// Typesense REST API прост и стабилен (ADR-023).
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// apiKeyHeader — имя HTTP-заголовка аутентификации Typesense (не секрет).
const apiKeyHeader = "X-TYPESENSE-API-KEY" //nolint:gosec // имя заголовка, а не учётные данные

// ErrNotFound — ресурс Typesense не найден (HTTP 404).
var ErrNotFound = errors.New("typesense: ресурс не найден")

// Client — минимальный клиент Typesense поверх net/http.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New создаёт клиент Typesense с заданным таймаутом на запрос.
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// Health проверяет доступность Typesense (GET /health → {"ok":true}).
func (c *Client) Health(ctx context.Context) error {
	status, body, err := c.do(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("typesense: health status %d: %s", status, body)
	}
	var h struct {
		Ok bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &h); err != nil {
		return fmt.Errorf("typesense: разбор health: %w", err)
	}
	if !h.Ok {
		return errors.New("typesense: health не ok")
	}
	return nil
}

// EnsureCollection создаёт коллекцию, если её ещё нет (идемпотентно). Существующая
// коллекция не пересоздаётся — схема считается стабильной; изменения схемы
// выполняются через пересоздание/переиндексацию (инструмент reindex, Stage 4.2).
func (c *Client) EnsureCollection(ctx context.Context, schema CollectionSchema) (created bool, err error) {
	_, err = c.getCollection(ctx, schema.Name)
	if err == nil {
		return false, nil // уже существует
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if err := c.createCollection(ctx, schema); err != nil {
		return false, err
	}
	return true, nil
}

// getCollection возвращает описание коллекции или ErrNotFound.
func (c *Client) getCollection(ctx context.Context, name string) ([]byte, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/collections/"+name, nil)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("typesense: получение коллекции %q status %d: %s", name, status, body)
	}
}

// createCollection создаёт коллекцию по схеме.
func (c *Client) createCollection(ctx context.Context, schema CollectionSchema) error {
	status, body, err := c.do(ctx, http.MethodPost, "/collections", schema)
	if err != nil {
		return err
	}
	// 201 — создана; 409 — уже существует (гонка при параллельном старте реплик).
	if status != http.StatusCreated && status != http.StatusConflict {
		return fmt.Errorf("typesense: создание коллекции %q status %d: %s", schema.Name, status, body)
	}
	return nil
}

// UpsertDocument вставляет/обновляет документ коллекции по его id (поле "id"
// внутри doc). Идемпотентно: повторный upsert перезаписывает документ.
func (c *Client) UpsertDocument(ctx context.Context, collection string, doc any) error {
	status, body, err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/documents?action=upsert", doc)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("typesense: upsert в %q status %d: %s", collection, status, body)
	}
	return nil
}

// DeleteDocument удаляет документ по id. Идемпотентно: отсутствие документа (404)
// считается успехом.
func (c *Client) DeleteDocument(ctx context.Context, collection, id string) error {
	status, body, err := c.do(ctx, http.MethodDelete, "/collections/"+collection+"/documents/"+id, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("typesense: удаление документа %q status %d: %s", id, status, body)
	}
	return nil
}

// do выполняет HTTP-запрос к Typesense, сериализуя body в JSON (если не nil),
// и возвращает статус и тело ответа.
func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("typesense: сериализация запроса: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("typesense: сборка запроса: %w", err)
	}
	req.Header.Set(apiKeyHeader, c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("typesense: запрос %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("typesense: чтение ответа: %w", err)
	}
	return resp.StatusCode, respBody, nil
}
