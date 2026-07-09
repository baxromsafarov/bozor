// Package transport собирает HTTP-слой Listing-сервиса. Создание требует
// аутентификации (владелец из forwarded-идентичности gateway); чтение публично.
package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/listing/internal/app"
	"bozor/services/listing/internal/domain"
)

// maxBodyBytes — предел тела запроса создания объявления.
const maxBodyBytes = 64 << 10

// Service — use-cases объявлений (реализуется app.Service).
type Service interface {
	Create(ctx context.Context, in app.CreateInput) (domain.Ad, error)
	Get(ctx context.Context, id string) (domain.Ad, error)
}

// Handler обслуживает эндпоинты объявлений.
type Handler struct {
	svc Service
	log *slog.Logger
}

// NewHandler создаёт обработчик объявлений.
func NewHandler(svc Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

type attributeDTO struct {
	Slug  string `json:"slug"`
	Value string `json:"value"`
}

type imageDTO struct {
	MediaID   string `json:"media_id"`
	SortOrder int    `json:"sort_order"`
	IsCover   bool   `json:"is_cover"`
}

type createRequest struct {
	CategoryID   string         `json:"category_id"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Price        int64          `json:"price"`
	Currency     string         `json:"currency"`
	RegionID     int16          `json:"region_id"`
	CityID       *int64         `json:"city_id"`
	Lat          *float64       `json:"lat"`
	Lng          *float64       `json:"lng"`
	PhoneDisplay bool           `json:"phone_display"`
	Attributes   []attributeDTO `json:"attributes"`
	Images       []imageDTO     `json:"images"`
}

type adResponse struct {
	ID           string         `json:"id"`
	UserID       string         `json:"user_id"`
	CategoryID   string         `json:"category_id"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Price        int64          `json:"price"`
	Currency     string         `json:"currency"`
	RegionID     int16          `json:"region_id"`
	CityID       *int64         `json:"city_id,omitempty"`
	Lat          *float64       `json:"lat,omitempty"`
	Lng          *float64       `json:"lng,omitempty"`
	Status       string         `json:"status"`
	PhoneDisplay bool           `json:"phone_display"`
	Attributes   []attributeDTO `json:"attributes"`
	Images       []imageDTO     `json:"images"`
	CreatedAt    string         `json:"created_at"`
}

// Create принимает объявление, валидирует и создаёт его в статусе draft.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
			"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
		return
	}

	var req createRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	ad, err := h.svc.Create(r.Context(), toCreateInput(owner, req))
	if err != nil {
		h.writeAdError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toResponse(ad))
}

// Get отдаёт объявление по id (публично).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ad, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writeAdError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toResponse(ad))
}

// toCreateInput переводит запрос в вход use-case.
func toCreateInput(owner string, req createRequest) app.CreateInput {
	in := app.CreateInput{
		UserID: owner, CategoryID: req.CategoryID, Title: req.Title, Description: req.Description,
		Price: req.Price, Currency: req.Currency, RegionID: req.RegionID, CityID: req.CityID,
		Lat: req.Lat, Lng: req.Lng, PhoneDisplay: req.PhoneDisplay,
	}
	for _, a := range req.Attributes {
		in.Attributes = append(in.Attributes, domain.AdAttributeValue{AttributeSlug: a.Slug, Value: a.Value})
	}
	for _, img := range req.Images {
		in.Images = append(in.Images, domain.AdImage{MediaID: img.MediaID, SortOrder: img.SortOrder, IsCover: img.IsCover})
	}
	return in
}

// toResponse строит ответ API из объявления.
func toResponse(a domain.Ad) adResponse {
	resp := adResponse{
		ID: a.ID, UserID: a.UserID, CategoryID: a.CategoryID, Title: a.Title, Description: a.Description,
		Price: a.Price, Currency: a.Currency, RegionID: a.RegionID, CityID: a.CityID, Lat: a.Lat, Lng: a.Lng,
		Status: string(a.Status), PhoneDisplay: a.PhoneDisplay,
		Attributes: make([]attributeDTO, 0, len(a.Attributes)),
		Images:     make([]imageDTO, 0, len(a.Images)),
		CreatedAt:  a.CreatedAt.UTC().Format(time.RFC3339),
	}
	for _, v := range a.Attributes {
		resp.Attributes = append(resp.Attributes, attributeDTO{Slug: v.AttributeSlug, Value: v.Value})
	}
	for _, img := range a.Images {
		resp.Images = append(resp.Images, imageDTO{MediaID: img.MediaID, SortOrder: img.SortOrder, IsCover: img.IsCover})
	}
	return resp
}

// writeAdError переводит доменные/прикладные ошибки в ответы RFC 7807.
func (h *Handler) writeAdError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrAdNotFound):
		problem(w, r, apperr.KindNotFound, "ad_not_found", "Объявление не найдено", "E'lon topilmadi")
	case errors.Is(err, app.ErrCategoryNotFound):
		problem(w, r, apperr.KindInvalid, "category_not_found", "Категория не найдена", "Turkum topilmadi")
	case errors.Is(err, domain.ErrUnknownAttribute):
		problem(w, r, apperr.KindInvalid, "unknown_attribute", "Неизвестный атрибут для категории", "Turkum uchun noma'lum atribut")
	case errors.Is(err, domain.ErrMissingRequiredAttr):
		problem(w, r, apperr.KindInvalid, "missing_required_attribute", "Не заполнен обязательный атрибут", "Majburiy atribut to'ldirilmagan")
	case errors.Is(err, domain.ErrInvalidAttrValue):
		problem(w, r, apperr.KindInvalid, "invalid_attribute_value", "Недопустимое значение атрибута", "Atribut qiymati yaroqsiz")
	case errors.Is(err, domain.ErrTooManyImages), errors.Is(err, domain.ErrMultipleCovers):
		problem(w, r, apperr.KindInvalid, "invalid_images", "Недопустимый набор изображений", "Rasmlar to'plami yaroqsiz")
	case errors.Is(err, domain.ErrEmptyTitle), errors.Is(err, domain.ErrTitleTooLong),
		errors.Is(err, domain.ErrNegativePrice), errors.Is(err, domain.ErrMissingCategory),
		errors.Is(err, domain.ErrMissingRegion):
		problem(w, r, apperr.KindInvalid, "invalid_ad", "Некорректные поля объявления", "E'lon maydonlari noto'g'ri")
	default:
		h.log.ErrorContext(r.Context(), "ошибка объявления", slog.String("error", err.Error()))
		problem(w, r, apperr.KindInternal, "ad_failed", "Внутренняя ошибка", "Ichki xatolik")
	}
}

// problem — сокращение для отправки RFC 7807.
func problem(w http.ResponseWriter, r *http.Request, kind apperr.Kind, code, msgRU, msgUZ string) {
	httpx.WriteProblem(w, r, apperr.New(kind, code, msgRU, msgUZ))
}
