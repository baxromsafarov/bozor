// Package transport собирает HTTP-слой User/Profile-сервиса. Правка профиля и
// настройки — только владелец (из проброшенной gateway идентичности); публичный
// профиль продавца доступен всем.
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

	"bozor/services/user-profile/internal/app"
	"bozor/services/user-profile/internal/domain"
)

// maxBodyBytes — предел тела запросов записи.
const maxBodyBytes = 16 << 10

// Service — use-cases профиля (реализуется app.Service).
type Service interface {
	Me(ctx context.Context, userID string) (domain.Profile, error)
	UpdateMe(ctx context.Context, userID string, in app.UpdateInput) (domain.Profile, error)
	PublicProfile(ctx context.Context, userID string) (app.PublicProfile, error)
	NotificationPrefs(ctx context.Context, userID string) ([]domain.NotificationPref, error)
	SetNotificationPrefs(ctx context.Context, userID string, prefs []domain.NotificationPref) ([]domain.NotificationPref, error)
}

// Handler обслуживает эндпоинты профиля и настроек.
type Handler struct {
	svc Service
	log *slog.Logger
}

// NewHandler создаёт обработчик профиля.
func NewHandler(svc Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

type ratingDTO struct {
	AvgRating    float64 `json:"avg_rating"`
	ReviewsCount int     `json:"reviews_count"`
}

// profileResponse — приватный профиль владельца (GET/PATCH /me).
type profileResponse struct {
	UserID              string  `json:"user_id"`
	DisplayName         string  `json:"display_name"`
	AvatarMediaID       *string `json:"avatar_media_id,omitempty"`
	About               string  `json:"about"`
	UserType            string  `json:"user_type"`
	BusinessName        string  `json:"business_name,omitempty"`
	CityID              *int64  `json:"city_id,omitempty"`
	ContactPhoneVisible bool    `json:"contact_phone_visible"`
	LanguageCode        string  `json:"language_code"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

// publicProfileResponse — публичный профиль продавца (GET /users/{id}).
type publicProfileResponse struct {
	UserID              string    `json:"user_id"`
	DisplayName         string    `json:"display_name"`
	AvatarMediaID       *string   `json:"avatar_media_id,omitempty"`
	About               string    `json:"about"`
	UserType            string    `json:"user_type"`
	BusinessName        string    `json:"business_name,omitempty"`
	CityID              *int64    `json:"city_id,omitempty"`
	ContactPhoneVisible bool      `json:"contact_phone_visible"`
	Rating              ratingDTO `json:"rating"`
	MemberSince         string    `json:"member_since"`
}

type updateRequest struct {
	DisplayName         *string `json:"display_name"`
	AvatarMediaID       *string `json:"avatar_media_id"`
	About               *string `json:"about"`
	UserType            *string `json:"user_type"`
	BusinessName        *string `json:"business_name"`
	CityID              *int64  `json:"city_id"`
	ContactPhoneVisible *bool   `json:"contact_phone_visible"`
}

// Me отдаёт профиль текущего пользователя (лениво создаёт при первом обращении).
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		h.unauthorized(w, r)
		return
	}
	p, err := h.svc.Me(r.Context(), owner)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toProfileResponse(p))
}

// UpdateMe применяет частичное изменение профиля владельца.
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		h.unauthorized(w, r)
		return
	}
	var req updateRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	p, err := h.svc.UpdateMe(r.Context(), owner, toUpdateInput(req))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toProfileResponse(p))
}

// PublicProfile отдаёт публичный профиль продавца по id (публично).
func (h *Handler) PublicProfile(w http.ResponseWriter, r *http.Request) {
	pp, err := h.svc.PublicProfile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toPublicResponse(pp))
}

func toUpdateInput(req updateRequest) app.UpdateInput {
	return app.UpdateInput{
		DisplayName:         req.DisplayName,
		AvatarMediaID:       req.AvatarMediaID,
		About:               req.About,
		UserType:            req.UserType,
		BusinessName:        req.BusinessName,
		CityID:              req.CityID,
		ContactPhoneVisible: req.ContactPhoneVisible,
	}
}

func toProfileResponse(p domain.Profile) profileResponse {
	return profileResponse{
		UserID: p.UserID, DisplayName: p.DisplayName, AvatarMediaID: p.AvatarMediaID,
		About: p.About, UserType: string(p.UserType), BusinessName: p.BusinessName,
		CityID: p.CityID, ContactPhoneVisible: p.ContactPhoneVisible, LanguageCode: p.LanguageCode,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toPublicResponse(pp app.PublicProfile) publicProfileResponse {
	p := pp.Profile
	return publicProfileResponse{
		UserID: p.UserID, DisplayName: p.DisplayName, AvatarMediaID: p.AvatarMediaID,
		About: p.About, UserType: string(p.UserType), BusinessName: p.BusinessName,
		CityID: p.CityID, ContactPhoneVisible: p.ContactPhoneVisible,
		Rating:      ratingDTO{AvgRating: pp.Rating.AvgRating, ReviewsCount: pp.Rating.ReviewsCount},
		MemberSince: p.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *Handler) unauthorized(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
		"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
}

// writeError переводит доменные/прикладные ошибки в ответы RFC 7807.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrProfileNotFound):
		problem(w, r, apperr.KindNotFound, "profile_not_found", "Профиль не найден", "Profil topilmadi")
	case errors.Is(err, domain.ErrInvalidUserType):
		problem(w, r, apperr.KindInvalid, "invalid_user_type", "Недопустимый тип аккаунта", "Hisob turi yaroqsiz")
	case errors.Is(err, domain.ErrBusinessNameRequired):
		problem(w, r, apperr.KindInvalid, "business_name_required", "Укажите название бизнеса", "Biznes nomini kiriting")
	case errors.Is(err, domain.ErrInvalidAvatar):
		problem(w, r, apperr.KindInvalid, "invalid_avatar", "Недопустимый аватар", "Avatar yaroqsiz")
	case errors.Is(err, domain.ErrDisplayNameTooLong), errors.Is(err, domain.ErrAboutTooLong),
		errors.Is(err, domain.ErrBusinessNameTooLong):
		problem(w, r, apperr.KindInvalid, "invalid_profile", "Некорректные поля профиля", "Profil maydonlari noto'g'ri")
	case errors.Is(err, domain.ErrInvalidNotificationPref):
		problem(w, r, apperr.KindInvalid, "invalid_notification_pref", "Недопустимая настройка уведомления", "Bildirishnoma sozlamasi yaroqsiz")
	default:
		h.log.ErrorContext(r.Context(), "ошибка профиля", slog.String("error", err.Error()))
		problem(w, r, apperr.KindInternal, "profile_failed", "Внутренняя ошибка", "Ichki xatolik")
	}
}

// problem — сокращение для отправки RFC 7807.
func problem(w http.ResponseWriter, r *http.Request, kind apperr.Kind, code, msgRU, msgUZ string) {
	httpx.WriteProblem(w, r, apperr.New(kind, code, msgRU, msgUZ))
}
