package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/apperr"
)

type decodeTarget struct {
	Name  string `json:"name"`
	Price int    `json:"price"`
}

func TestDecodeJSON(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		maxBytes int64
		wantErr  bool
	}{
		{name: "валидное тело", body: `{"name":"olma","price":100}`, maxBytes: 1024, wantErr: false},
		{name: "неизвестное поле", body: `{"name":"olma","extra":true}`, maxBytes: 1024, wantErr: true},
		{name: "пустое тело", body: ``, maxBytes: 1024, wantErr: true},
		{name: "два JSON-значения", body: `{"name":"a"}{"name":"b"}`, maxBytes: 1024, wantErr: true},
		{name: "превышение размера", body: `{"name":"` + strings.Repeat("x", 100) + `"}`, maxBytes: 16, wantErr: true},
		{name: "битый синтаксис", body: `{"name":`, maxBytes: 1024, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(tt.body))
			w := httptest.NewRecorder()

			var dst decodeTarget
			err := DecodeJSON(w, r, &dst, tt.maxBytes)

			if !tt.wantErr {
				require.NoError(t, err)
				assert.Equal(t, "olma", dst.Name)
				assert.Equal(t, 100, dst.Price)
				return
			}

			require.Error(t, err)
			var appErr *apperr.Error
			require.ErrorAs(t, err, &appErr, "ошибка должна быть *apperr.Error")
			assert.Equal(t, apperr.KindInvalid, appErr.Kind)
			assert.Equal(t, "invalid_json", appErr.Code)
			assert.Equal(t, "Некорректный JSON в теле запроса", appErr.MsgRU)
			assert.Equal(t, "So'rov tanasida noto'g'ri JSON", appErr.MsgUZ)
		})
	}
}

func TestDecodeJSON_MaxBytesWrapped(t *testing.T) {
	// Причина превышения размера должна быть доступна через errors.As.
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("1", 64)))
	err := DecodeJSON(httptest.NewRecorder(), r, new(any), 8)
	require.Error(t, err)

	var maxErr *http.MaxBytesError
	assert.True(t, errors.As(err, &maxErr), "исходная MaxBytesError должна быть в цепочке")
}

func TestRespond(t *testing.T) {
	t.Run("с телом", func(t *testing.T) {
		w := httptest.NewRecorder()
		Respond(w, http.StatusCreated, map[string]string{"id": "42"})

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))

		var body map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, "42", body["id"])
	})

	t.Run("nil — только статус", func(t *testing.T) {
		w := httptest.NewRecorder()
		Respond(w, http.StatusNoContent, nil)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Zero(t, w.Body.Len())
	})
}
