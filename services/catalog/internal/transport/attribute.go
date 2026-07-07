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

// AttributeService — use-cases атрибутов (реализуется app.AttributeService).
type AttributeService interface {
	List(ctx context.Context) ([]domain.Attribute, error)
	Get(ctx context.Context, id string) (domain.Attribute, error)
	Create(ctx context.Context, in app.CreateAttributeInput) (domain.Attribute, error)
	Update(ctx context.Context, id string, in app.UpdateAttributeInput) (domain.Attribute, error)
	Delete(ctx context.Context, id string) error
	Effective(ctx context.Context, categoryID string) ([]domain.EffectiveAttribute, error)
	Link(ctx context.Context, categoryID, attributeID string, sortOrder int) error
	Unlink(ctx context.Context, categoryID, attributeID string) error
}

// AttributeHandler обслуживает эндпоинты атрибутов.
type AttributeHandler struct {
	svc AttributeService
	log *slog.Logger
}

// NewAttributeHandler создаёт обработчик атрибутов.
func NewAttributeHandler(svc AttributeService, log *slog.Logger) *AttributeHandler {
	return &AttributeHandler{svc: svc, log: log}
}

type optionPayload struct {
	Slug      string `json:"slug"`
	NameUZ    string `json:"name_uz"`
	NameRU    string `json:"name_ru"`
	SortOrder int    `json:"sort_order"`
}

type attributeResponse struct {
	ID           string           `json:"id"`
	Slug         string           `json:"slug"`
	NameUZ       string           `json:"name_uz"`
	NameRU       string           `json:"name_ru"`
	Type         string           `json:"type"`
	Unit         *string          `json:"unit,omitempty"`
	IsRequired   bool             `json:"is_required"`
	IsFilterable bool             `json:"is_filterable"`
	Options      []optionResponse `json:"options,omitempty"`
}

type optionResponse struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	NameUZ    string `json:"name_uz"`
	NameRU    string `json:"name_ru"`
	SortOrder int    `json:"sort_order"`
}

type effectiveAttributeResponse struct {
	attributeResponse
	Inherited bool   `json:"inherited"`
	SortOrder int    `json:"sort_order"`
	SourceID  string `json:"source_id"`
}

// List отдаёт все определения атрибутов (публично).
func (h *AttributeHandler) List(w http.ResponseWriter, r *http.Request) {
	attrs, err := h.svc.List(r.Context())
	if err != nil {
		writeCatalogError(w, r, h.log, err)
		return
	}
	out := make([]attributeResponse, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, toAttributeResponse(a))
	}
	httpx.Respond(w, http.StatusOK, map[string]any{"attributes": out})
}

// Get отдаёт атрибут по id (публично).
func (h *AttributeHandler) Get(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeCatalogError(w, r, h.log, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toAttributeResponse(a))
}

type attributeRequest struct {
	Slug         string          `json:"slug"`
	NameUZ       string          `json:"name_uz"`
	NameRU       string          `json:"name_ru"`
	Type         string          `json:"type"`
	Unit         *string         `json:"unit"`
	IsRequired   bool            `json:"is_required"`
	IsFilterable bool            `json:"is_filterable"`
	Options      []optionPayload `json:"options"`
}

// Create создаёт атрибут (только персонал).
func (h *AttributeHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req attributeRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.Slug == "" || req.NameUZ == "" || req.NameRU == "" || req.Type == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_attribute",
			"Обязательны slug, name_uz, name_ru, type", "slug, name_uz, name_ru, type majburiy"))
		return
	}

	a, err := h.svc.Create(r.Context(), app.CreateAttributeInput{
		Slug: req.Slug, NameUZ: req.NameUZ, NameRU: req.NameRU,
		Type: domain.AttributeType(req.Type), Unit: req.Unit,
		IsRequired: req.IsRequired, IsFilterable: req.IsFilterable,
		Options: toOptionInputs(req.Options),
	})
	if err != nil {
		writeCatalogError(w, r, h.log, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toAttributeResponse(a))
}

type attributeUpdateRequest struct {
	NameUZ       string          `json:"name_uz"`
	NameRU       string          `json:"name_ru"`
	Unit         *string         `json:"unit"`
	IsRequired   bool            `json:"is_required"`
	IsFilterable bool            `json:"is_filterable"`
	Options      []optionPayload `json:"options"`
}

// Update меняет изменяемые поля атрибута (только персонал).
func (h *AttributeHandler) Update(w http.ResponseWriter, r *http.Request) {
	var req attributeUpdateRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.NameUZ == "" || req.NameRU == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_attribute",
			"Обязательны name_uz, name_ru", "name_uz, name_ru majburiy"))
		return
	}

	a, err := h.svc.Update(r.Context(), chi.URLParam(r, "id"), app.UpdateAttributeInput{
		NameUZ: req.NameUZ, NameRU: req.NameRU, Unit: req.Unit,
		IsRequired: req.IsRequired, IsFilterable: req.IsFilterable,
		Options: toOptionInputs(req.Options),
	})
	if err != nil {
		writeCatalogError(w, r, h.log, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toAttributeResponse(a))
}

// Delete удаляет атрибут (только персонал).
func (h *AttributeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeCatalogError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CategoryAttributes отдаёт эффективные атрибуты категории (публично).
func (h *AttributeHandler) CategoryAttributes(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Effective(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeCatalogError(w, r, h.log, err)
		return
	}
	out := make([]effectiveAttributeResponse, 0, len(items))
	for _, e := range items {
		out = append(out, effectiveAttributeResponse{
			attributeResponse: toAttributeResponse(e.Attribute),
			Inherited:         e.Inherited,
			SortOrder:         e.SortOrder,
			SourceID:          e.SourceID,
		})
	}
	httpx.Respond(w, http.StatusOK, map[string]any{"attributes": out})
}

type linkRequest struct {
	AttributeID string `json:"attribute_id"`
	SortOrder   int    `json:"sort_order"`
}

// Link привязывает атрибут к категории (только персонал).
func (h *AttributeHandler) Link(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.AttributeID == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_link",
			"Обязателен attribute_id", "attribute_id majburiy"))
		return
	}
	if err := h.svc.Link(r.Context(), chi.URLParam(r, "id"), req.AttributeID, req.SortOrder); err != nil {
		writeCatalogError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unlink снимает привязку атрибута с категории (только персонал).
func (h *AttributeHandler) Unlink(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Unlink(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "attributeId")); err != nil {
		writeCatalogError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toOptionInputs(in []optionPayload) []app.OptionInput {
	if len(in) == 0 {
		return nil
	}
	out := make([]app.OptionInput, len(in))
	for i, o := range in {
		out[i] = app.OptionInput{Slug: o.Slug, NameUZ: o.NameUZ, NameRU: o.NameRU, SortOrder: o.SortOrder}
	}
	return out
}

func toAttributeResponse(a domain.Attribute) attributeResponse {
	resp := attributeResponse{
		ID: a.ID, Slug: a.Slug, NameUZ: a.NameUZ, NameRU: a.NameRU,
		Type: string(a.Type), Unit: a.Unit,
		IsRequired: a.IsRequired, IsFilterable: a.IsFilterable,
	}
	for _, o := range a.Options {
		resp.Options = append(resp.Options, optionResponse{
			ID: o.ID, Slug: o.Slug, NameUZ: o.NameUZ, NameRU: o.NameRU, SortOrder: o.SortOrder,
		})
	}
	return resp
}
