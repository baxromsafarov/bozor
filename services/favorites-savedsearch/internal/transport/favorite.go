// Package transport собирает HTTP-слой Favorites/SavedSearch-сервиса. Все
// операции — только владелец (из проброшенной gateway идентичности).
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

	"bozor/services/favorites-savedsearch/internal/domain"
)

// Service — use-cases избранного (реализуется app.Service).
type Service interface {
	Add(ctx context.Context, userID, adID string) (domain.Favorite, error)
	Remove(ctx context.Context, userID, adID string) error
	List(ctx context.Context, userID string, limit, offset int) ([]domain.Favorite, error)
}

// Handler обслуживает эндпоинты избранного.
type Handler struct {
	svc Service
	log *slog.Logger
}

// NewHandler создаёт обработчик избранного.
func NewHandler(svc Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

type favoriteDTO struct {
	AdID      string `json:"ad_id"`
	CreatedAt string `json:"created_at"`
}

type favoritesListResponse struct {
	Favorites []favoriteDTO `json:"favorites"`
	Limit     int           `json:"limit"`
	Offset    int           `json:"offset"`
}

// Add добавляет объявление в избранное владельца.
func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		h.unauthorized(w, r)
		return
	}
	fav, err := h.svc.Add(r.Context(), owner, chi.URLParam(r, "adId"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, favoriteDTO{AdID: fav.AdID, CreatedAt: fav.CreatedAt.UTC().Format(time.RFC3339)})
}

// Remove удаляет объявление из избранного владельца (идемпотентно, 204).
func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		h.unauthorized(w, r)
		return
	}
	if err := h.svc.Remove(r.Context(), owner, chi.URLParam(r, "adId")); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// List отдаёт избранное владельца (пагинация).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		h.unauthorized(w, r)
		return
	}
	limit, offset := pageParams(r.URL.Query())
	favs, err := h.svc.List(r.Context(), owner, limit, offset)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toListResponse(favs, limit, offset))
}

func toListResponse(favs []domain.Favorite, limit, offset int) favoritesListResponse {
	out := favoritesListResponse{Favorites: make([]favoriteDTO, 0, len(favs)), Limit: limit, Offset: offset}
	for _, f := range favs {
		out.Favorites = append(out.Favorites, favoriteDTO{AdID: f.AdID, CreatedAt: f.CreatedAt.UTC().Format(time.RFC3339)})
	}
	return out
}

// pageParams извлекает limit/offset из query (клампинг — в use-case).
func pageParams(q url.Values) (limit, offset int) {
	return atoiDefault(q.Get("limit"), 0), atoiDefault(q.Get("offset"), 0)
}

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

func (h *Handler) unauthorized(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
		"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
}

// writeError переводит доменные ошибки в ответы RFC 7807.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidAdID):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_ad_id",
			"Некорректный идентификатор объявления", "E'lon identifikatori noto'g'ri"))
	default:
		h.log.ErrorContext(r.Context(), "ошибка избранного", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "favorites_failed",
			"Внутренняя ошибка", "Ichki xatolik"))
	}
}
