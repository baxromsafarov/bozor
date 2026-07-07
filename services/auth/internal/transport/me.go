package transport

import (
	"net/http"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/httpx"
)

// MeHandler отдаёт идентичность текущего пользователя. Это защищённый маршрут:
// gateway проверяет access-JWT и проставляет X-User-Id/X-User-Roles, которые
// читает authx.FromForwardedHeaders; при их отсутствии — 401.
type MeHandler struct{}

// NewMeHandler создаёт обработчик /me.
func NewMeHandler() *MeHandler { return &MeHandler{} }

type meResponse struct {
	UserID string   `json:"user_id"`
	Roles  []string `json:"roles"`
}

// Me возвращает user_id и роли аутентифицированного пользователя.
func (h *MeHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID := authx.UserID(r.Context())
	if userID == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindUnauthorized, "unauthorized",
			"Требуется авторизация", "Avtorizatsiya talab qilinadi"))
		return
	}
	roles := authx.Roles(r.Context())
	if roles == nil {
		roles = []string{}
	}
	httpx.Respond(w, http.StatusOK, meResponse{UserID: userID, Roles: roles})
}
