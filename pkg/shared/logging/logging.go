// Package logging предоставляет структурированное JSON-логирование на базе slog
// с автоматическим добавлением request_id из контекста и trace_id/span_id
// из активного OpenTelemetry-спана.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// New создаёт JSON-логгер, пишущий в os.Stdout, с постоянным атрибутом
// service=<service> и уровнем, разобранным из level (см. ParseLevel).
func New(service, level string) *slog.Logger {
	return newWithWriter(os.Stdout, service, level)
}

// newWithWriter — внутренний конструктор: как New, но с произвольным io.Writer.
// Используется в тестах для записи в буфер.
func newWithWriter(w io.Writer, service, level string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: ParseLevel(level)})
	logger := slog.New(&contextHandler{inner: h})
	return logger.With(slog.String("service", service))
}

// ParseLevel разбирает строковое имя уровня логирования
// ("debug", "info", "warn", "error", регистр не важен).
// Для неизвестных значений возвращается slog.LevelInfo.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// contextHandler — обёртка над slog.Handler, добавляющая в каждую запись
// атрибуты request_id (из контекста, см. WithRequestID) и trace_id/span_id
// из валидного OpenTelemetry SpanContext.
type contextHandler struct {
	inner slog.Handler
}

// Enabled сообщает, обрабатывается ли запись данного уровня.
func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle добавляет контекстные атрибуты и передаёт запись внутреннему handler'у.
func (h *contextHandler) Handle(ctx context.Context, rec slog.Record) error {
	if id := RequestID(ctx); id != "" {
		rec.AddAttrs(slog.String("request_id", id))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		rec.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, rec)
}

// WithAttrs возвращает новый handler с добавленными атрибутами.
func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup возвращает новый handler с открытой группой name.
func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name)}
}
