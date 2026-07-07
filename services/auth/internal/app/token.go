package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"

	"bozor/services/auth/internal/domain"
)

// RefreshStore — персистентность refresh-токенов (реализуется repo.RefreshRepo).
type RefreshStore interface {
	Insert(ctx context.Context, in domain.RefreshInsert) error
	Rotate(ctx context.Context, oldHash []byte, expectedDevice, newID string, newHash []byte, newExpiresAt time.Time) (domain.RotationResult, error)
	RevokeFamily(ctx context.Context, hash []byte) (userID string, found bool, err error)
}

// Auditor пишет журнал чувствительных действий (реализуется repo.AuditRepo).
// Аудит best-effort: его ошибка логируется, но не срывает основную операцию.
type Auditor interface {
	Log(ctx context.Context, e domain.AuditEntry) error
}

// TokenService выпускает и ротирует токены: access-JWT через authx.Signer,
// refresh — случайный непрозрачный токен, хранимый хешем.
type TokenService struct {
	signer     *authx.Signer
	refresh    RefreshStore
	auditor    Auditor
	refreshTTL time.Duration
	log        *slog.Logger
}

// NewTokenService создаёт сервис токенов. auditor может быть nil (аудит выкл).
func NewTokenService(signer *authx.Signer, refresh RefreshStore, auditor Auditor, refreshTTL time.Duration, log *slog.Logger) *TokenService {
	return &TokenService{signer: signer, refresh: refresh, auditor: auditor, refreshTTL: refreshTTL, log: log}
}

// audit пишет запись журнала best-effort (ошибка не срывает операцию).
func (s *TokenService) audit(ctx context.Context, e domain.AuditEntry) {
	if s.auditor == nil {
		return
	}
	if err := s.auditor.Log(ctx, e); err != nil && s.log != nil {
		s.log.WarnContext(ctx, "не удалось записать аудит",
			slog.String("event", e.Event), slog.String("error", err.Error()))
	}
}

// IssueForUser выпускает новую пару токенов для пользователя (новое семейство
// refresh). Пустой deviceID заменяется сгенерированным — клиент получает его в
// ответе и предъявляет при последующем refresh. clientIP пишется в аудит login.
func (s *TokenService) IssueForUser(ctx context.Context, userID, deviceID, clientIP string) (domain.TokenPair, error) {
	if deviceID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return domain.TokenPair{}, apperr.Wrap(err, apperr.KindInternal, "device_id_gen",
				"Внутренняя ошибка", "Ichki xatolik")
		}
		deviceID = id.String()
	}

	familyID, err := uuid.NewV7()
	if err != nil {
		return domain.TokenPair{}, apperr.Wrap(err, apperr.KindInternal, "family_id_gen",
			"Внутренняя ошибка", "Ichki xatolik")
	}
	rowID, err := uuid.NewV7()
	if err != nil {
		return domain.TokenPair{}, apperr.Wrap(err, apperr.KindInternal, "token_id_gen",
			"Внутренняя ошибка", "Ichki xatolik")
	}

	token, hash, err := domain.NewRefreshToken()
	if err != nil {
		return domain.TokenPair{}, apperr.Wrap(err, apperr.KindInternal, "refresh_gen",
			"Внутренняя ошибка", "Ichki xatolik")
	}

	if err := s.refresh.Insert(ctx, domain.RefreshInsert{
		ID:        rowID.String(),
		UserID:    userID,
		TokenHash: hash,
		DeviceID:  deviceID,
		FamilyID:  familyID.String(),
		ExpiresAt: time.Now().Add(s.refreshTTL),
	}); err != nil {
		return domain.TokenPair{}, apperr.Wrap(err, apperr.KindInternal, "refresh_persist",
			"Внутренняя ошибка", "Ichki xatolik")
	}

	s.audit(ctx, domain.AuditEntry{
		UserID: userID, Event: domain.AuditLogin, IP: clientIP,
		Detail: map[string]any{"device_id": deviceID},
	})
	return s.buildPair(userID, deviceID, token)
}

// Refresh ротирует предъявленный refresh-токен и выдаёт новую пару. Старый
// токен гасится; повторное использование отзывает всё семейство.
func (s *TokenService) Refresh(ctx context.Context, refreshToken, deviceID, clientIP string) (domain.TokenPair, error) {
	newToken, newHash, err := domain.NewRefreshToken()
	if err != nil {
		return domain.TokenPair{}, apperr.Wrap(err, apperr.KindInternal, "refresh_gen",
			"Внутренняя ошибка", "Ichki xatolik")
	}
	newID, err := uuid.NewV7()
	if err != nil {
		return domain.TokenPair{}, apperr.Wrap(err, apperr.KindInternal, "token_id_gen",
			"Внутренняя ошибка", "Ichki xatolik")
	}

	res, err := s.refresh.Rotate(ctx, domain.HashRefreshToken(refreshToken), deviceID,
		newID.String(), newHash, time.Now().Add(s.refreshTTL))
	if err != nil {
		return domain.TokenPair{}, mapRotateError(err)
	}

	s.audit(ctx, domain.AuditEntry{
		UserID: res.UserID, Event: domain.AuditTokenRefreshed, IP: clientIP,
		Detail: map[string]any{"device_id": res.DeviceID},
	})
	return s.buildPair(res.UserID, res.DeviceID, newToken)
}

// Logout отзывает семейство refresh-токена (все устройства текущей сессии).
// Идемпотентен: неизвестный/уже отозванный токен не считается ошибкой.
func (s *TokenService) Logout(ctx context.Context, refreshToken, clientIP string) error {
	userID, found, err := s.refresh.RevokeFamily(ctx, domain.HashRefreshToken(refreshToken))
	if err != nil {
		return apperr.Wrap(err, apperr.KindInternal, "logout_failed",
			"Внутренняя ошибка", "Ichki xatolik")
	}
	if found {
		s.audit(ctx, domain.AuditEntry{UserID: userID, Event: domain.AuditLogout, IP: clientIP})
	}
	return nil
}

// buildPair подписывает access-токен и собирает пару для отдачи клиенту.
func (s *TokenService) buildPair(userID, deviceID, refreshToken string) (domain.TokenPair, error) {
	access, _, expiresAt, err := s.signer.Sign(userID, []string{domain.RoleUser})
	if err != nil {
		return domain.TokenPair{}, apperr.Wrap(err, apperr.KindInternal, "access_sign",
			"Внутренняя ошибка", "Ichki xatolik")
	}
	return domain.TokenPair{
		AccessToken:  access,
		RefreshToken: refreshToken,
		ExpiresIn:    int(time.Until(expiresAt).Seconds()),
		TokenType:    domain.TokenTypeBearer,
		DeviceID:     deviceID,
		UserID:       userID,
	}, nil
}

// mapRotateError переводит доменные ошибки ротации в HTTP-ошибки.
func mapRotateError(err error) error {
	switch {
	case errors.Is(err, domain.ErrTokenReuse):
		return apperr.Wrap(err, apperr.KindUnauthorized, "token_reuse_detected",
			"Сессия отозвана из соображений безопасности", "Sessiya xavfsizlik uchun bekor qilindi")
	case errors.Is(err, domain.ErrTokenNotFound),
		errors.Is(err, domain.ErrTokenExpired),
		errors.Is(err, domain.ErrDeviceMismatch):
		return apperr.Wrap(err, apperr.KindUnauthorized, "invalid_refresh_token",
			"Недействительный refresh-токен", "Yaroqsiz refresh-token")
	default:
		return apperr.Wrap(err, apperr.KindInternal, "refresh_failed",
			"Внутренняя ошибка", "Ichki xatolik")
	}
}
