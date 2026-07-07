package transport

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/catalog/internal/app"
	"bozor/services/catalog/internal/domain"
)

// maxBody — предел размера тела запросов записи (16 KiB).
const maxBody = 16 << 10

// Service — use-cases каталога (реализуется app.Service).
type Service interface {
	TreeJSON(ctx context.Context) ([]byte, error)
	Create(ctx context.Context, in app.CreateInput) (domain.Category, error)
	Update(ctx context.Context, id string, in app.UpdateInput) (domain.Category, error)
	Delete(ctx context.Context, id string) error
}

// Handler обслуживает эндпоинты каталога.
type Handler struct {
	svc Service
	log *slog.Logger
}

// NewHandler создаёт обработчик каталога.
func NewHandler(svc Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Tree отдаёт дерево категорий (публично, из кеша). Тело уже сериализовано;
// добавляем ETag/Cache-Control для условных запросов.
func (h *Handler) Tree(w http.ResponseWriter, r *http.Request) {
	body, err := h.svc.TreeJSON(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "дерево категорий", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "tree_failed",
			"Не удалось получить категории", "Kategoriyalarni olib bo'lmadi"))
		return
	}
	writeCachedJSON(w, r, body)
}

type createRequest struct {
	ParentID  *string `json:"parent_id"`
	Slug      string  `json:"slug"`
	NameUZ    string  `json:"name_uz"`
	NameRU    string  `json:"name_ru"`
	SortOrder int     `json:"sort_order"`
	IsActive  *bool   `json:"is_active"`
}

type categoryResponse struct {
	ID        string  `json:"id"`
	ParentID  *string `json:"parent_id,omitempty"`
	Slug      string  `json:"slug"`
	NameUZ    string  `json:"name_uz"`
	NameRU    string  `json:"name_ru"`
	Level     int     `json:"level"`
	Path      string  `json:"path"`
	SortOrder int     `json:"sort_order"`
	IsActive  bool    `json:"is_active"`
}

// Create создаёт категорию (только персонал).
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.Slug == "" || req.NameUZ == "" || req.NameRU == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_category",
			"Обязательны slug, name_uz, name_ru", "slug, name_uz, name_ru majburiy"))
		return
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}

	cat, err := h.svc.Create(r.Context(), app.CreateInput{
		ParentID: req.ParentID, Slug: req.Slug, NameUZ: req.NameUZ, NameRU: req.NameRU,
		SortOrder: req.SortOrder, IsActive: active,
	})
	if err != nil {
		h.writeCategoryError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toResponse(cat))
}

type updateRequest struct {
	NameUZ    string `json:"name_uz"`
	NameRU    string `json:"name_ru"`
	SortOrder int    `json:"sort_order"`
	IsActive  *bool  `json:"is_active"`
}

// Update меняет изменяемые поля категории (только персонал).
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req updateRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.NameUZ == "" || req.NameRU == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_category",
			"Обязательны name_uz, name_ru", "name_uz, name_ru majburiy"))
		return
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}

	cat, err := h.svc.Update(r.Context(), id, app.UpdateInput{
		NameUZ: req.NameUZ, NameRU: req.NameRU, SortOrder: req.SortOrder, IsActive: active,
	})
	if err != nil {
		h.writeCategoryError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toResponse(cat))
}

// Delete удаляет категорию (только персонал).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.writeCategoryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeCategoryError переводит доменные ошибки в HTTP-ответы RFC 7807.
func (h *Handler) writeCategoryError(w http.ResponseWriter, r *http.Request, err error) {
	writeCatalogError(w, r, h.log, err)
}

func toResponse(c domain.Category) categoryResponse {
	return categoryResponse{
		ID: c.ID, ParentID: c.ParentID, Slug: c.Slug, NameUZ: c.NameUZ, NameRU: c.NameRU,
		Level: c.Level, Path: c.Path, SortOrder: c.SortOrder, IsActive: c.IsActive,
	}
}
