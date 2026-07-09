// Package transport собирает HTTP-слой Listing-сервиса. Создание требует
// аутентификации (владелец из forwarded-идентичности gateway); чтение публично.
package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
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
	Update(ctx context.Context, adID, userID string, in app.UpdateInput) (domain.Ad, error)
	Delete(ctx context.Context, adID, userID string) error
	Feed(ctx context.Context, f domain.FeedFilter) ([]domain.Ad, error)
	MyAds(ctx context.Context, userID, status string, limit, offset int) ([]domain.Ad, error)
	SubmitForModeration(ctx context.Context, adID, userID string) (domain.Ad, error)
	MarkSold(ctx context.Context, adID, userID string) (domain.Ad, error)
	Renew(ctx context.Context, adID, userID string) (domain.Ad, error)
	Archive(ctx context.Context, adID, userID string) (domain.Ad, error)
	ExportByID(ctx context.Context, id string) (domain.Ad, error)
	ExportActive(ctx context.Context, after string, limit int) ([]domain.Ad, error)
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

type updateRequest struct {
	Title        *string         `json:"title"`
	Description  *string         `json:"description"`
	Price        *int64          `json:"price"`
	Currency     *string         `json:"currency"`
	CategoryID   *string         `json:"category_id"`
	RegionID     *int16          `json:"region_id"`
	CityID       *int64          `json:"city_id"`
	Lat          *float64        `json:"lat"`
	Lng          *float64        `json:"lng"`
	PhoneDisplay *bool           `json:"phone_display"`
	Attributes   *[]attributeDTO `json:"attributes"`
	Images       *[]imageDTO     `json:"images"`
}

type listResponse struct {
	Ads    []adResponse `json:"ads"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
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
	ViewsCount   int64          `json:"views_count"`
	Attributes   []attributeDTO `json:"attributes"`
	Images       []imageDTO     `json:"images"`
	PublishedAt  string         `json:"published_at,omitempty"`
	ExpiresAt    string         `json:"expires_at,omitempty"`
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

// Update изменяет объявление владельца (частичный PATCH).
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
			"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
		return
	}
	var req updateRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	ad, err := h.svc.Update(r.Context(), chi.URLParam(r, "id"), owner, toUpdateInput(req))
	if err != nil {
		h.writeAdError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toResponse(ad))
}

// Delete удаляет объявление владельца.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
			"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
		return
	}
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id"), owner); err != nil {
		h.writeAdError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Feed отдаёт ленту активных объявлений (публично, пагинация/фильтры/сортировка).
func (h *Handler) Feed(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := pageParams(q)
	f := domain.FeedFilter{
		CategoryID: q.Get("category_id"),
		RegionID:   parseRegionID(q.Get("region_id")),
		Sort:       q.Get("sort"),
		Limit:      limit,
		Offset:     offset,
	}
	ads, err := h.svc.Feed(r.Context(), f)
	if err != nil {
		h.writeAdError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toListResponse(ads, limit, offset))
}

// MyAds отдаёт объявления текущего пользователя (по статусу, пагинация).
func (h *Handler) MyAds(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
			"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	if status != "" && !domain.Status(status).Valid() {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_status",
			"Неизвестный статус", "Noma'lum holat"))
		return
	}
	limit, offset := pageParams(q)
	ads, err := h.svc.MyAds(r.Context(), owner, status, limit, offset)
	if err != nil {
		h.writeAdError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toListResponse(ads, limit, offset))
}

// Submit отправляет объявление владельца на модерацию (draft|rejected → pending).
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	h.lifecycle(w, r, h.svc.SubmitForModeration)
}

// Sold помечает активное объявление владельца проданным (active → sold).
func (h *Handler) Sold(w http.ResponseWriter, r *http.Request) {
	h.lifecycle(w, r, h.svc.MarkSold)
}

// Renew продлевает срок объявления владельца (active — сдвиг, expired — реактивация).
func (h *Handler) Renew(w http.ResponseWriter, r *http.Request) {
	h.lifecycle(w, r, h.svc.Renew)
}

// Archive архивирует активное объявление владельца (active → archived).
func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	h.lifecycle(w, r, h.svc.Archive)
}

// lifecycle — общий каркас действий над жизненным циклом: требует владельца из
// проброшенной идентичности (401 анониму), вызывает use-case и отдаёт объявление.
func (h *Handler) lifecycle(w http.ResponseWriter, r *http.Request,
	action func(ctx context.Context, adID, userID string) (domain.Ad, error),
) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
			"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
		return
	}
	ad, err := action(r.Context(), chi.URLParam(r, "id"), owner)
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

// toUpdateInput переводит частичный запрос PATCH во вход use-case.
func toUpdateInput(req updateRequest) app.UpdateInput {
	in := app.UpdateInput{
		Title: req.Title, Description: req.Description, Price: req.Price, Currency: req.Currency,
		CategoryID: req.CategoryID, RegionID: req.RegionID, CityID: req.CityID,
		Lat: req.Lat, Lng: req.Lng, PhoneDisplay: req.PhoneDisplay,
	}
	if req.Attributes != nil {
		attrs := make([]domain.AdAttributeValue, 0, len(*req.Attributes))
		for _, a := range *req.Attributes {
			attrs = append(attrs, domain.AdAttributeValue{AttributeSlug: a.Slug, Value: a.Value})
		}
		in.Attributes = &attrs
	}
	if req.Images != nil {
		imgs := make([]domain.AdImage, 0, len(*req.Images))
		for _, img := range *req.Images {
			imgs = append(imgs, domain.AdImage{MediaID: img.MediaID, SortOrder: img.SortOrder, IsCover: img.IsCover})
		}
		in.Images = &imgs
	}
	return in
}

// toListResponse собирает ответ ленты/списка из объявлений.
func toListResponse(ads []domain.Ad, limit, offset int) listResponse {
	out := listResponse{Ads: make([]adResponse, 0, len(ads)), Limit: limit, Offset: offset}
	for _, a := range ads {
		out.Ads = append(out.Ads, toResponse(a))
	}
	return out
}

// pageParams извлекает limit/offset из query (валидация/клампинг — в use-case).
func pageParams(q url.Values) (limit, offset int) {
	return atoiDefault(q.Get("limit"), 0), atoiDefault(q.Get("offset"), 0)
}

// atoiDefault парсит целое из строки, возвращая def при пустом/некорректном значении.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// parseRegionID парсит region_id как smallint (0 — любой регион / некорректное значение).
func parseRegionID(s string) int16 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 16)
	if err != nil {
		return 0
	}
	return int16(n)
}

// toResponse строит ответ API из объявления.
func toResponse(a domain.Ad) adResponse {
	resp := adResponse{
		ID: a.ID, UserID: a.UserID, CategoryID: a.CategoryID, Title: a.Title, Description: a.Description,
		Price: a.Price, Currency: a.Currency, RegionID: a.RegionID, CityID: a.CityID, Lat: a.Lat, Lng: a.Lng,
		Status: string(a.Status), PhoneDisplay: a.PhoneDisplay, ViewsCount: a.ViewsCount,
		Attributes:  make([]attributeDTO, 0, len(a.Attributes)),
		Images:      make([]imageDTO, 0, len(a.Images)),
		PublishedAt: formatTime(a.PublishedAt),
		ExpiresAt:   formatTime(a.ExpiresAt),
		CreatedAt:   a.CreatedAt.UTC().Format(time.RFC3339),
	}
	for _, v := range a.Attributes {
		resp.Attributes = append(resp.Attributes, attributeDTO{Slug: v.AttributeSlug, Value: v.Value})
	}
	for _, img := range a.Images {
		resp.Images = append(resp.Images, imageDTO{MediaID: img.MediaID, SortOrder: img.SortOrder, IsCover: img.IsCover})
	}
	return resp
}

// formatTime форматирует опциональную отметку времени в RFC 3339 (пусто для nil).
func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// writeAdError переводит доменные/прикладные ошибки в ответы RFC 7807.
func (h *Handler) writeAdError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrAdNotFound):
		problem(w, r, apperr.KindNotFound, "ad_not_found", "Объявление не найдено", "E'lon topilmadi")
	case errors.Is(err, app.ErrForbidden):
		problem(w, r, apperr.KindForbidden, "forbidden", "Нет прав на объявление", "E'longa huquq yo'q")
	case errors.Is(err, domain.ErrInvalidTransition):
		problem(w, r, apperr.KindConflict, "invalid_transition", "Недопустимый переход статуса", "Holatni bunday o'zgartirib bo'lmaydi")
	case errors.Is(err, domain.ErrNotEditable):
		problem(w, r, apperr.KindConflict, "not_editable", "Объявление нельзя редактировать в текущем статусе", "E'lonni bu holatda tahrirlab bo'lmaydi")
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
