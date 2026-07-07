package transport

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/auth/internal/domain"
)

// maxTokenBody — предел размера тела запросов токенов (8 KiB).
const maxTokenBody = 8 << 10

// deviceIDRe — допустимый формат device_id (клиентский идентификатор устройства).
var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)

// Tokens — выпуск и ротация токенов (реализуется app.TokenService).
type Tokens interface {
	IssueForUser(ctx context.Context, userID, deviceID string) (domain.TokenPair, error)
	Refresh(ctx context.Context, refreshToken, deviceID string) (domain.TokenPair, error)
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

	pair, err := h.tokens.Refresh(r.Context(), req.RefreshToken, req.DeviceID)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, toTokenResponse(pair))
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
