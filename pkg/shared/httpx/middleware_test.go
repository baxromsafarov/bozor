package httpx

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/logging"
)

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
	}{
		{name: "входящий заголовок сохраняется", incoming: "req-123"},
		{name: "без заголовка генерируется uuid", incoming: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotCtxID string
			h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotCtxID = logging.RequestID(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}))

			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tt.incoming != "" {
				r.Header.Set("X-Request-Id", tt.incoming)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			headerID := w.Header().Get("X-Request-Id")
			require.NotEmpty(t, headerID)
			assert.Equal(t, headerID, gotCtxID, "id в контексте и в заголовке ответа должны совпадать")

			if tt.incoming != "" {
				assert.Equal(t, tt.incoming, headerID)
			} else {
				parsed, err := uuid.Parse(headerID)
				require.NoError(t, err, "сгенерированный id должен быть валидным uuid")
				assert.Equal(t, uuid.Version(7), parsed.Version())
			}
		})
	}
}

func TestRecover(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("panic превращается в 500 problem+json", func(t *testing.T) {
		h := Recover(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("что-то пошло не так")
		}))

		r := httptest.NewRequest(http.MethodGet, "/boom", nil)
		w := httptest.NewRecorder()
		require.NotPanics(t, func() { h.ServeHTTP(w, r) })

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")

		var p apperr.Problem
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
		assert.Equal(t, http.StatusInternalServerError, p.Status)
		assert.Equal(t, "https://bozor.uz/problems/panic", p.Type)
		assert.Equal(t, "Внутренняя ошибка сервера", p.Detail)
		assert.Equal(t, "/boom", p.Instance)
	})

	t.Run("http.ErrAbortHandler пробрасывается дальше", func(t *testing.T) {
		h := Recover(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		}))

		r := httptest.NewRequest(http.MethodGet, "/abort", nil)
		w := httptest.NewRecorder()
		assert.PanicsWithValue(t, http.ErrAbortHandler, func() { h.ServeHTTP(w, r) })
	})
}

func TestAccessLog(t *testing.T) {
	var buf logBuffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	h := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	}))

	r := httptest.NewRequest(http.MethodPost, "/items", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "POST", entry["method"])
	assert.Equal(t, "/items", entry["path"])
	assert.EqualValues(t, http.StatusCreated, entry["status"])
	assert.EqualValues(t, 5, entry["bytes"])
	assert.Contains(t, entry, "duration_ms")
}

// logBuffer — простой потокобезопасный буфер для перехвата вывода логгера.
type logBuffer struct {
	data []byte
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *logBuffer) Bytes() []byte { return b.data }
