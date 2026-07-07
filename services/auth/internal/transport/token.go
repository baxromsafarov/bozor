package transport

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/auth/internal/domain"
)

// maxTokenBody — предел размера тела запросов токенов (8 KiB).
const maxTokenBody = 8 << 10

// deviceIDRe — допустимый формат device_id (клиентский идентификатор устройства).
var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)

// Tokens — выпуск, ротация и отзыв токенов (реализуется app.TokenService).
type Tokens interface {
	IssueForUser(ctx context.Context, userID, deviceID, clientIP string) (domain.TokenPair, error)
	Refresh(ctx context.Context, refreshToken, deviceID, clientIP string) (domain.TokenPair, error)
	Logout(ctx context.Context, refreshToken, clientIP string) error
}

// clientIP извлекает клиентский IP: первый хоп X-Forwarded-For (проставляет
// gateway/nginx), затем X-Real-Ip, иначе адрес соединения.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-Ip"); xr != "" {
		return strings.TrimSpace(xr)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// TokenHandler обслуживает обмен refresh-токена на новую пару.
type TokenHandler struct {
	tokens Tokens
	log    *slog.Logger
}

// NewTokenHandler создаёт обработчик токенов.
func NewTokenHandler(tokens Tokens, log *slog.Logger) *TokenHandler {
	return &TokenHandler{tokens: tokens, log: log}
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	DeviceID     string `json:"device_id,omitempty"`
	UserID       string `json:"user_id,omitempty"`
}

// Refresh обменивает refresh-токен на новую пару (ротация).
func (h *TokenHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := httpx.DecodeJSON(w, r, &req, maxTokenBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.RefreshToken == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "missing_refresh_token",
			"Не передан refresh-токен", "Refresh-token yuborilmadi"))
		return
	}
	if !deviceIDRe.MatchString(req.DeviceID) {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_device_id",
			"Некорректный идентификатор устройства", "Yaroqsiz qurilma identifikatori"))
		return
	}

	pair, err := h.tokens.Refresh(r.Context(), req.RefreshToken, req.DeviceID, clientIP(r))
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toTokenResponse(pair))
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout отзывает семейство refresh-токена (выход). Идемпотентен: неизвестный
// токен тоже даёт 204, чтобы не раскрывать существование сессии.
func (h *TokenHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	if err := httpx.DecodeJSON(w, r, &req, maxTokenBody); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.RefreshToken == "" {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "missing_refresh_token",
			"Не передан refresh-токен", "Refresh-token yuborilmadi"))
		return
	}
	if err := h.tokens.Logout(r.Context(), req.RefreshToken, clientIP(r)); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// toTokenResponse собирает JSON-ответ из пары токенов.
func toTokenResponse(p domain.TokenPair) tokenResponse {
	return tokenResponse{
		AccessToken:  p.AccessToken,
		RefreshToken: p.RefreshToken,
		TokenType:    p.TokenType,
		ExpiresIn:    p.ExpiresIn,
		DeviceID:     p.DeviceID,
		UserID:       p.UserID,
	}
}
