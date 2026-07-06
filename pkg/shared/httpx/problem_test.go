package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/logging"
)

func TestWriteProblem(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		lang       string
		wantStatus int
		wantType   string
		wantDetail string
	}{
		{
			name:       "not found на русском",
			err:        apperr.New(apperr.KindNotFound, "product_not_found", "Товар не найден", "Mahsulot topilmadi"),
			lang:       "ru",
			wantStatus: http.StatusNotFound,
			wantType:   "https://bozor.uz/problems/product_not_found",
			wantDetail: "Товар не найден",
		},
		{
			name:       "локализация uz",
			err:        apperr.New(apperr.KindNotFound, "product_not_found", "Товар не найден", "Mahsulot topilmadi"),
			lang:       "uz",
			wantStatus: http.StatusNotFound,
			wantType:   "https://bozor.uz/problems/product_not_found",
			wantDetail: "Mahsulot topilmadi",
		},
		{
			name:       "валидация 422",
			err:        apperr.New(apperr.KindInvalid, "invalid_json", "Некорректный JSON в теле запроса", "So'rov tanasida noto'g'ri JSON"),
			lang:       "uz",
			wantStatus: http.StatusUnprocessableEntity,
			wantType:   "https://bozor.uz/problems/invalid_json",
			wantDetail: "So'rov tanasida noto'g'ri JSON",
		},
		{
			name:       "неизвестная ошибка — 500 без деталей",
			err:        errors.New("secret database failure"),
			lang:       "ru",
			wantStatus: http.StatusInternalServerError,
			wantType:   "about:blank",
			wantDetail: "Внутренняя ошибка сервера",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/products/1", nil)
			ctx := logging.WithRequestID(r.Context(), "req-777")
			r = r.WithContext(ctx)
			if tt.lang == "uz" {
				r.Header.Set("Accept-Language", "uz-UZ")
			}

			w := httptest.NewRecorder()
			// Прогоняем через Lang, чтобы язык попал в контекст.
			Lang(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				WriteProblem(w, r, tt.err)
			})).ServeHTTP(w, r)

			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")

			var p apperr.Problem
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
			assert.Equal(t, tt.wantStatus, p.Status)
			assert.Equal(t, tt.wantType, p.Type)
			assert.Equal(t, tt.wantDetail, p.Detail)
			assert.Equal(t, "/api/v1/products/1", p.Instance)
			assert.Equal(t, "req-777", p.RequestID)
			assert.Equal(t, http.StatusText(tt.wantStatus), p.Title)
		})
	}
}
