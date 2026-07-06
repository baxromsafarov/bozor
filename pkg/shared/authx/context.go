package authx

import "context"

// ctxKey — приватный тип ключей контекста пакета authx.
type ctxKey int

const (
	userIDKey ctxKey = iota // идентификатор пользователя (claims.Subject)
	rolesKey                // роли пользователя
	jtiKey                  // идентификатор токена (jti)
)

// withAuth кладёт данные аутентифицированного пользователя в контекст.
// Пустой jti (например, при заголовках от gateway) не записывается.
func withAuth(ctx context.Context, userID string, roles []string, jti string) context.Context {
	ctx = context.WithValue(ctx, userIDKey, userID)
	ctx = context.WithValue(ctx, rolesKey, roles)
	if jti != "" {
		ctx = context.WithValue(ctx, jtiKey, jti)
	}
	return ctx
}

// UserID возвращает идентификатор пользователя из контекста
// или пустую строку, если пользователь не аутентифицирован.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

// Roles возвращает роли пользователя из контекста или nil.
func Roles(ctx context.Context) []string {
	roles, _ := ctx.Value(rolesKey).([]string)
	return roles
}

// JTI возвращает идентификатор токена (jti) из контекста
// или пустую строку, если токен не проверялся.
func JTI(ctx context.Context) string {
	jti, _ := ctx.Value(jtiKey).(string)
	return jti
}

// HasRole сообщает, есть ли у пользователя из контекста роль role.
func HasRole(ctx context.Context, role string) bool {
	for _, r := range Roles(ctx) {
		if r == role {
			return true
		}
	}
	return false
}
