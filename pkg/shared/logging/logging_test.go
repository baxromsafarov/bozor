package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want slog.Level
	}{
		{name: "debug", in: "debug", want: slog.LevelDebug},
		{name: "info", in: "info", want: slog.LevelInfo},
		{name: "warn", in: "warn", want: slog.LevelWarn},
		{name: "error", in: "error", want: slog.LevelError},
		{name: "верхний регистр", in: "ERROR", want: slog.LevelError},
		{name: "смешанный регистр", in: "Warn", want: slog.LevelWarn},
		{name: "пробелы вокруг", in: " debug ", want: slog.LevelDebug},
		{name: "неизвестный — Info", in: "verbose", want: slog.LevelInfo},
		{name: "пустая строка — Info", in: "", want: slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseLevel(tt.in))
		})
	}
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "не задан", ctx: context.Background(), want: ""},
		{name: "задан", ctx: WithRequestID(context.Background(), "req-123"), want: "req-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, RequestID(tt.ctx))
		})
	}
}

// logLine парсит одну JSON-строку лога в map.
func logLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	return m
}

func TestLoggerJSONFields(t *testing.T) {
	tests := []struct {
		name          string
		ctx           context.Context
		wantRequestID string
		hasRequestID  bool
	}{
		{
			name:         "без request_id",
			ctx:          context.Background(),
			hasRequestID: false,
		},
		{
			name:          "с request_id",
			ctx:           WithRequestID(context.Background(), "req-42"),
			wantRequestID: "req-42",
			hasRequestID:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := newWithWriter(&buf, "orders", "debug")
			logger.InfoContext(tt.ctx, "hello", slog.String("k", "v"))

			m := logLine(t, &buf)
			assert.Equal(t, "orders", m["service"])
			assert.Equal(t, "hello", m["msg"])
			assert.Equal(t, "v", m["k"])
			if tt.hasRequestID {
				assert.Equal(t, tt.wantRequestID, m["request_id"])
			} else {
				assert.NotContains(t, m, "request_id")
			}
			// Без валидного SpanContext поля трассировки отсутствуют.
			assert.NotContains(t, m, "trace_id")
			assert.NotContains(t, m, "span_id")
		})
	}
}

func TestLoggerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := newWithWriter(&buf, "svc", "warn")

	logger.Info("skipped")
	assert.Zero(t, buf.Len(), "info должен фильтроваться на уровне warn")

	logger.Warn("written")
	m := logLine(t, &buf)
	assert.Equal(t, "written", m["msg"])
	assert.Equal(t, "svc", m["service"])
}

func TestLoggerWithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := newWithWriter(&buf, "svc", "info").
		With(slog.String("extra", "x")).
		WithGroup("grp")

	ctx := WithRequestID(context.Background(), "rid-1")
	logger.InfoContext(ctx, "grouped", slog.String("inner", "y"))

	m := logLine(t, &buf)
	assert.Equal(t, "svc", m["service"])
	assert.Equal(t, "x", m["extra"])
	grp, ok := m["grp"].(map[string]any)
	require.True(t, ok, "группа grp должна присутствовать")
	assert.Equal(t, "y", grp["inner"])
	// request_id добавляется contextHandler'ом до группировки JSONHandler'а,
	// поэтому попадает внутрь открытой группы.
	assert.Equal(t, "rid-1", grp["request_id"])
}
