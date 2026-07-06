// Package authx содержит утилиты аутентификации Bozor: выпуск и проверку
// JWT (HS256), HTTP-middleware авторизации и доступ к данным пользователя
// через context.Context.
package authx

import "github.com/golang-jwt/jwt/v5"

// Claims — полезная нагрузка JWT-токена Bozor: роли пользователя
// плюс стандартные зарегистрированные клеймы (sub, iss, jti, iat, exp).
type Claims struct {
	Roles []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}
