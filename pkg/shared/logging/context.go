package logging

import "context"

// ctxKey — приватный тип ключа контекста, исключающий коллизии с другими пакетами.
type ctxKey struct{}

// requestIDKey — ключ, под которым в контексте хранится идентификатор запроса.
var requestIDKey = ctxKey{}

// WithRequestID возвращает контекст с сохранённым идентификатором запроса id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID возвращает идентификатор запроса из контекста
// или пустую строку, если он не задан.
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}
