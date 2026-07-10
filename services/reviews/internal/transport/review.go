package transport

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/reviews/internal/app"
	"bozor/services/reviews/internal/domain"
)

// maxReviewBody — предел тела запроса создания отзыва.
const maxReviewBody = 8 * 1024

// ReviewHandler — HTTP-обработчики отзывов.
type ReviewHandler struct {
	svc *app.Service
	log *slog.Logger
}

// NewReviewHandler создаёт обработчик отзывов.
func NewReviewHandler(svc *app.Service, log *slog.Logger) *ReviewHandler {
	return &ReviewHandler{svc: svc, log: log}
}

type createReviewRequest struct {
	AdID   string `json:"ad_id"`
	Rating int    `json:"rating"`
	Body   string `json:"body"`
}

type reviewDTO struct {
	ID        string `json:"id"`
	AdID      string `json:"ad_id"`
	AuthorID  string `json:"author_id"`
	TargetID  string `json:"target_id"`
	Rating    int    `json:"rating"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type reviewsResponse struct {
	Reviews []reviewDTO `json:"reviews"`
}

// ratingResponse — агрегат рейтинга продавца (внутренний эндпоинт для Profile 9.2).
type ratingResponse struct {
	UserID       string  `json:"user_id"`
	AvgRating    float64 `json:"avg_rating"`
	ReviewsCount int     `json:"reviews_count"`
}

// Create создаёт отзыв текущего пользователя о продавце по объявлению.
func (h *ReviewHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createReviewRequest
	if err := httpx.DecodeJSON(w, r, &req, maxReviewBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	rev, err := h.svc.Create(r.Context(), app.CreateInput{
		AuthorID: authx.UserID(r.Context()),
		AdID:     req.AdID,
		Rating:   req.Rating,
		Body:     req.Body,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusCreated, toDTO(rev))
}

// ListByUser отдаёт активные отзывы о пользователе (свежие сверху) с пагинацией.
func (h *ReviewHandler) ListByUser(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "userID")
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	reviews, err := h.svc.ListByUser(r.Context(), targetID, limit, offset)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := reviewsResponse{Reviews: make([]reviewDTO, 0, len(reviews))}
	for _, rev := range reviews {
		out.Reviews = append(out.Reviews, toDTO(rev))
	}
	httpx.Respond(w, http.StatusOK, out)
}

// Rating отдаёт агрегат рейтинга продавца по активным отзывам. Внутренний
// эндпоинт (только сеть compose, без пользовательской авторизации — как
// /internal/ads Listing): его читает агрегатор рейтинга Profile (9.2).
func (h *ReviewHandler) Rating(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	rt, err := h.svc.Rating(r.Context(), userID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, ratingResponse{
		UserID: userID, AvgRating: rt.AvgRating, ReviewsCount: rt.ReviewsCount,
	})
}

// writeError переводит доменную ошибку в ответ RFC 7807.
func (h *ReviewHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidRating):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_rating",
			"Оценка должна быть от 1 до 5", "Baho 1 dan 5 gacha bo'lishi kerak"))
	case errors.Is(err, domain.ErrBodyTooLong):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "body_too_long",
			"Текст отзыва слишком длинный", "Sharh matni juda uzun"))
	case errors.Is(err, domain.ErrAdNotFound):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindNotFound, "ad_not_found",
			"Объявление не найдено", "E'lon topilmadi"))
	case errors.Is(err, domain.ErrSelfReview):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "self_review",
			"Нельзя оставить отзыв о своём объявлении", "O'z e'loningizga sharh qoldirib bo'lmaydi"))
	case errors.Is(err, domain.ErrDuplicateReview):
		httpx.WriteProblem(w, r, apperr.New(apperr.KindConflict, "duplicate_review",
			"Вы уже оставили отзыв по этому объявлению", "Siz bu e'longa allaqachon sharh qoldirgansiz"))
	default:
		h.log.ErrorContext(r.Context(), "ошибка отзыва", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "internal_error",
			"Внутренняя ошибка", "Ichki xatolik"))
	}
}

// toDTO проецирует отзыв в DTO ответа.
func toDTO(rev domain.Review) reviewDTO {
	return reviewDTO{
		ID: rev.ID, AdID: rev.AdID, AuthorID: rev.AuthorID, TargetID: rev.TargetID,
		Rating: rev.Rating, Body: rev.Body, CreatedAt: rev.CreatedAt.UTC().Format(time.RFC3339),
	}
}
