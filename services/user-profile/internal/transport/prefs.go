package transport

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"

	"bozor/services/user-profile/internal/domain"
)

type prefDTO struct {
	Channel   string `json:"channel"`
	EventType string `json:"event_type"`
	Enabled   bool   `json:"enabled"`
}

type prefsResponse struct {
	Prefs []prefDTO `json:"prefs"`
}

type putPrefsRequest struct {
	Prefs []prefDTO `json:"prefs"`
}

// GetPrefs отдаёт эффективные настройки уведомлений владельца.
func (h *Handler) GetPrefs(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		h.unauthorized(w, r)
		return
	}
	prefs, err := h.svc.NotificationPrefs(r.Context(), owner)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toPrefsResponse(prefs))
}

// PutPrefs заменяет набор настроек уведомлений владельца.
func (h *Handler) PutPrefs(w http.ResponseWriter, r *http.Request) {
	owner := authx.UserID(r.Context())
	if owner == "" {
		h.unauthorized(w, r)
		return
	}
	var req putPrefsRequest
	if err := httpx.DecodeJSON(w, r, &req, maxBodyBytes); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	prefs, err := h.svc.SetNotificationPrefs(r.Context(), owner, toPrefs(req.Prefs))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toPrefsResponse(prefs))
}

// InternalPrefs отдаёт эффективные настройки уведомлений пользователя по id для
// внутренних потребителей (Notification-сервис). Без авторизации: эндпоинт
// доступен только во внутренней сети compose (как /internal/ads Listing).
func (h *Handler) InternalPrefs(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_user_id",
			"Некорректный идентификатор пользователя", "Foydalanuvchi identifikatori noto'g'ri"))
		return
	}
	prefs, err := h.svc.NotificationPrefs(r.Context(), userID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toPrefsResponse(prefs))
}

func toPrefs(dtos []prefDTO) []domain.NotificationPref {
	out := make([]domain.NotificationPref, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, domain.NotificationPref{Channel: d.Channel, EventType: d.EventType, Enabled: d.Enabled})
	}
	return out
}

func toPrefsResponse(prefs []domain.NotificationPref) prefsResponse {
	out := prefsResponse{Prefs: make([]prefDTO, 0, len(prefs))}
	for _, p := range prefs {
		out.Prefs = append(out.Prefs, prefDTO{Channel: p.Channel, EventType: p.EventType, Enabled: p.Enabled})
	}
	return out
}
