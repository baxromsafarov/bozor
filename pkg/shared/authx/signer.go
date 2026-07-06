package authx

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"bozor/pkg/shared/apperr"
)

// Signer выпускает access-токены JWT, подписанные алгоритмом HS256.
type Signer struct {
	key       []byte        // секретный ключ подписи
	issuer    string        // издатель токена (iss)
	accessTTL time.Duration // время жизни access-токена
}

// NewSigner создаёт Signer с секретным ключом key, издателем issuer
// и временем жизни access-токена accessTTL.
func NewSigner(key []byte, issuer string, accessTTL time.Duration) *Signer {
	return &Signer{key: key, issuer: issuer, accessTTL: accessTTL}
}

// Sign выпускает подписанный HS256 access-токен для пользователя userID
// с ролями roles. В токен записываются: jti (UUID v7), Subject = userID,
// Issuer, IssuedAt и ExpiresAt в UTC. Возвращает строку токена, jti
// и момент истечения.
func (s *Signer) Sign(userID string, roles []string) (token, jti string, expiresAt time.Time, err error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("authx: генерация jti: %w", err)
	}
	jti = id.String()

	now := time.Now().UTC()
	expiresAt = now.Add(s.accessTTL)

	claims := &Claims{
		Roles: roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Subject:   userID,
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.key)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("authx: подпись токена: %w", err)
	}
	return token, jti, expiresAt, nil
}

// Parse разбирает и проверяет JWT-токен tokenStr ключом key.
// Допускаются только токены HS256; проверяются exp и iat с допуском 30 секунд.
// Любая ошибка разбора или проверки возвращается как доменная ошибка
// apperr.KindUnauthorized с кодом "invalid_token".
func Parse(key []byte, tokenStr string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(
		tokenStr,
		claims,
		func(*jwt.Token) (any, error) { return key, nil },
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithLeeway(30*time.Second),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return nil, apperr.Wrap(err, apperr.KindUnauthorized, "invalid_token",
			"Недействительный токен", "Yaroqsiz token")
	}
	return claims, nil
}
