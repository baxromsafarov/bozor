package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// RoleUser — роль обычного пользователя (по умолчанию при выпуске токена).
const RoleUser = "user"

// TokenTypeBearer — тип access-токена в ответах OAuth-style.
const TokenTypeBearer = "Bearer"

// refreshTokenBytes — длина случайного refresh-токена в байтах.
const refreshTokenBytes = 32

// Ошибки refresh-токенов.
var (
	// ErrTokenNotFound — предъявленный refresh-токен неизвестен.
	ErrTokenNotFound = errors.New("refresh-токен не найден")
	// ErrTokenExpired — refresh-токен истёк.
	ErrTokenExpired = errors.New("refresh-токен истёк")
	// ErrTokenReuse — повторное использование уже ротированного/отозванного
	// токена: признак кражи, всё семейство отзывается.
	ErrTokenReuse = errors.New("повторное использование refresh-токена")
	// ErrDeviceMismatch — refresh-токен предъявлен с чужого устройства.
	ErrDeviceMismatch = errors.New("refresh-токен не принадлежит устройству")
)

// TokenPair — выданная пара токенов доступа/обновления.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // секунды до истечения access-токена
	TokenType    string
	DeviceID     string
	UserID       string
}

// RefreshInsert — данные для вставки нового refresh-токена (хранится только хеш).
type RefreshInsert struct {
	ID        string
	UserID    string
	TokenHash []byte
	DeviceID  string
	FamilyID  string
	ExpiresAt time.Time
}

// RotationResult — данные исходного refresh-токена, нужные при ротации для
// выпуска нового access-токена.
type RotationResult struct {
	UserID   string
	FamilyID string
	DeviceID string
}

// NewRefreshToken генерирует случайный refresh-токен (base64url без padding)
// и его SHA-256 хеш для хранения. Сам токен в БД не сохраняется.
func NewRefreshToken() (token string, hash []byte, err error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("domain: генерация refresh-токена: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashRefreshToken(token), nil
}

// HashRefreshToken возвращает SHA-256 хеш refresh-токена для поиска/сравнения.
func HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
