package transport

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/httpx"

	"bozor/services/auth/internal/session"
)

// nonceRe — формат nonce: 32 hex-символа (16 случайных байт).
var nonceRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// SessionStore — хранилище nonce'ов логина (реализуется session.Store).
type SessionStore interface {
	Init(ctx context.Context) (nonce string, ttl time.Duration, err error)
	Get(ctx context.Context, nonce string) (session.Session, error)
	Consume(ctx context.Context, nonce string) (session.Session, error)
}

// SessionHandler обслуживает инициацию входа и опрос его статуса по nonce.
// При подтверждённом входе выдаёт пару токенов (завершение логина).
type SessionHandler struct {
	sessions    SessionStore
	tokens      Tokens
	botUsername string // username бота для сборки deep-link
	log         *slog.Logger
}

// NewSessionHandler создаёт обработчик сессий логина.
func NewSessionHandler(sessions SessionStore, tokens Tokens, botUsername string, log *slog.Logger) *SessionHandler {
	return &SessionHandler{sessions: sessions, tokens: tokens, botUsername: botUsername, log: log}
}

type initResponse struct {
	Nonce     string `json:"nonce"`
	DeepLink  string `json:"deep_link"`
	ExpiresIn int    `json:"expires_in"` // секунды до истечения nonce
}

// Init создаёт одноразовый nonce и возвращает deep-link на Telegram-бота.
func (h *SessionHandler) Init(w http.ResponseWriter, r *http.Request) {
	nonce, ttl, err := h.sessions.Init(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "инициация логин-сессии", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "session_init_failed",
			"Не удалось начать вход", "Kirishni boshlab bo'lmadi"))
		return
	}
	httpx.Respond(w, http.StatusCreated, initResponse{
		Nonce:     nonce,
		DeepLink:  deepLink(h.botUsername, nonce),
		ExpiresIn: int(ttl.Seconds()),
	})
}

type statusResponse struct {
	Status string `json:"status"` // pending | confirmed | expired
	UserID string `json:"user_id,omitempty"`

	// Токены присутствуют только при первом confirmed-опросе (см. Status).
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
}

// Status отдаёт текущий статус логина по nonce. При confirmed завершает вход:
// одноразово (Consume) выдаёт пару токенов и удаляет nonce. Необязательный
// query-параметр device_id привязывает refresh-токен к устройству; при
// отсутствии генерируется и возвращается новый идентификатор.
func (h *SessionHandler) Status(w http.ResponseWriter, r *http.Request) {
	nonce := chi.URLParam(r, "nonce")
	if !nonceRe.MatchString(nonce) {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_nonce",
			"Некорректный идентификатор сессии", "Yaroqsiz sessiya identifikatori"))
		return
	}

	sess, err := h.sessions.Get(r.Context(), nonce)
	if err != nil {
		h.log.ErrorContext(r.Context(), "опрос логин-сессии", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "session_status_failed",
			"Не удалось получить статус", "Holatni olib bo'lmadi"))
		return
	}
	if sess.Status != session.StatusConfirmed {
		httpx.Respond(w, http.StatusOK, statusResponse{Status: sess.Status})
		return
	}

	// Валидация device_id до потребления nonce, чтобы не «сжечь» его на 4xx.
	deviceID := r.URL.Query().Get("device_id")
	if deviceID != "" && !deviceIDRe.MatchString(deviceID) {
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInvalid, "invalid_device_id",
			"Некорректный идентификатор устройства", "Yaroqsiz qurilma identifikatori"))
		return
	}

	// Одноразовое завершение входа: забираем nonce атомарно.
	consumed, err := h.sessions.Consume(r.Context(), nonce)
	if err != nil {
		h.log.ErrorContext(r.Context(), "consume логин-сессии", slog.String("error", err.Error()))
		httpx.WriteProblem(w, r, apperr.New(apperr.KindInternal, "session_status_failed",
			"Не удалось получить статус", "Holatni olib bo'lmadi"))
		return
	}
	if consumed.Status != session.StatusConfirmed || consumed.UserID == "" {
		// Гонку выиграл другой опрос — токены уже выданы.
		httpx.Respond(w, http.StatusOK, statusResponse{Status: session.StatusExpired})
		return
	}

	pair, err := h.tokens.IssueForUser(r.Context(), consumed.UserID, deviceID)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	httpx.Respond(w, http.StatusOK, statusResponse{
		Status:       session.StatusConfirmed,
		UserID:       pair.UserID,
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		TokenType:    pair.TokenType,
		ExpiresIn:    pair.ExpiresIn,
		DeviceID:     pair.DeviceID,
	})
}

// deepLink собирает ссылку t.me/<bot>?start=<nonce>. Пустой username (бот не
// сконфигурирован в dev) даёт пустую ссылку — nonce всё равно возвращается.
func deepLink(botUsername, nonce string) string {
	if botUsername == "" {
		return ""
	}
	return "https://t.me/" + botUsername + "?start=" + nonce
}
