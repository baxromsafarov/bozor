package apperr

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPStatus проверяет маппинг категорий ошибок на HTTP-статусы.
func TestHTTPStatus(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want int
	}{
		{"invalid", KindInvalid, http.StatusUnprocessableEntity},
		{"not_found", KindNotFound, http.StatusNotFound},
		{"unauthorized", KindUnauthorized, http.StatusUnauthorized},
		{"forbidden", KindForbidden, http.StatusForbidden},
		{"conflict", KindConflict, http.StatusConflict},
		{"rate_limited", KindRateLimited, http.StatusTooManyRequests},
		{"unavailable", KindUnavailable, http.StatusServiceUnavailable},
		{"internal", KindInternal, http.StatusInternalServerError},
		{"unknown_kind", Kind(999), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HTTPStatus(tt.kind))
		})
	}
}

// TestErrorMsg проверяет локализацию сообщения ошибки.
func TestErrorMsg(t *testing.T) {
	tests := []struct {
		name  string
		msgRU string
		msgUZ string
		lang  string
		want  string
	}{
		{"ru", "Не найдено", "Topilmadi", "ru", "Не найдено"},
		{"uz", "Не найдено", "Topilmadi", "uz", "Topilmadi"},
		{"uz_fallback_ru", "Не найдено", "", "uz", "Не найдено"},
		{"unknown_lang", "Не найдено", "Topilmadi", "en", "Не найдено"},
		{"empty_lang", "Не найдено", "Topilmadi", "", "Не найдено"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(KindNotFound, "not_found", tt.msgRU, tt.msgUZ)
			assert.Equal(t, tt.want, e.Msg(tt.lang))
		})
	}
}

// TestErrorString проверяет формат Error().
func TestErrorString(t *testing.T) {
	t.Run("without_cause", func(t *testing.T) {
		e := New(KindConflict, "duplicate", "Дубликат", "")
		assert.Equal(t, "duplicate: Дубликат", e.Error())
	})
	t.Run("with_cause", func(t *testing.T) {
		cause := errors.New("pq: unique violation")
		e := Wrap(cause, KindConflict, "duplicate", "Дубликат", "")
		assert.Contains(t, e.Error(), "duplicate")
		assert.Contains(t, e.Error(), "Дубликат")
		assert.Contains(t, e.Error(), "pq: unique violation")
	})
}

// TestUnwrapChain проверяет Unwrap и работу errors.Is/errors.As по цепочке.
func TestUnwrapChain(t *testing.T) {
	sentinel := errors.New("sentinel")
	e := Wrap(sentinel, KindUnavailable, "db_down", "БД недоступна", "")

	require.ErrorIs(t, e, sentinel)
	assert.Equal(t, sentinel, e.Unwrap())

	var appErr *Error
	require.ErrorAs(t, e, &appErr)
	assert.Equal(t, KindUnavailable, appErr.Kind)

	// Ошибка без вложенной причины.
	assert.Nil(t, New(KindInternal, "x", "y", "").Unwrap())
}

// TestKindOf проверяет извлечение категории из цепочки ошибок.
func TestKindOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{"app_error", New(KindNotFound, "nf", "Не найдено", ""), KindNotFound},
		{"wrapped_deep", Wrap(New(KindForbidden, "fb", "Запрещено", ""), KindInternal, "in", "Ошибка", ""), KindInternal},
		{"plain_error", errors.New("boom"), KindInternal},
		{"nil", nil, KindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, KindOf(tt.err))
		})
	}
}

// TestWithFields проверяет накопление ошибок полей.
func TestWithFields(t *testing.T) {
	e := New(KindInvalid, "validation", "Ошибка валидации", "Validatsiya xatosi").
		WithFields(FieldError{Field: "name", Code: "required", MsgRU: "Обязательное поле", MsgUZ: "Majburiy maydon"}).
		WithFields(FieldError{Field: "price", Code: "min", MsgRU: "Слишком мало", MsgUZ: ""})

	require.Len(t, e.Fields, 2)
	assert.Equal(t, "name", e.Fields[0].Field)
	assert.Equal(t, "price", e.Fields[1].Field)
}

// TestFromErrorAppError проверяет построение Problem из *Error.
func TestFromErrorAppError(t *testing.T) {
	e := New(KindInvalid, "validation_failed", "Ошибка валидации", "Validatsiya xatosi").
		WithFields(
			FieldError{Field: "name", Code: "required", MsgRU: "Обязательное поле", MsgUZ: "Majburiy maydon"},
			FieldError{Field: "price", Code: "min", MsgRU: "Слишком мало", MsgUZ: ""},
		)

	tests := []struct {
		name       string
		lang       string
		wantDetail string
		wantMsg0   string
		wantMsg1   string
	}{
		{"ru", "ru", "Ошибка валидации", "Обязательное поле", "Слишком мало"},
		{"uz_with_fallback", "uz", "Validatsiya xatosi", "Majburiy maydon", "Слишком мало"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := FromError(e, "/api/v1/products", tt.lang, "req-123")

			assert.Equal(t, http.StatusUnprocessableEntity, p.Status)
			assert.Equal(t, http.StatusText(http.StatusUnprocessableEntity), p.Title)
			assert.Equal(t, "https://bozor.uz/problems/validation_failed", p.Type)
			assert.Equal(t, tt.wantDetail, p.Detail)
			assert.Equal(t, "/api/v1/products", p.Instance)
			assert.Equal(t, "req-123", p.RequestID)

			require.Len(t, p.Errors, 2)
			assert.Equal(t, "name", p.Errors[0].Field)
			assert.Equal(t, "required", p.Errors[0].Code)
			assert.Equal(t, tt.wantMsg0, p.Errors[0].Message)
			assert.Equal(t, tt.wantMsg1, p.Errors[1].Message)
		})
	}
}

// TestFromErrorEmptyCode проверяет Type="about:blank" при пустом Code.
func TestFromErrorEmptyCode(t *testing.T) {
	e := New(KindNotFound, "", "Не найдено", "")
	p := FromError(e, "", "ru", "")
	assert.Equal(t, "about:blank", p.Type)
	assert.Equal(t, http.StatusNotFound, p.Status)
	assert.Empty(t, p.Errors)
}

// TestFromErrorWrappedAppError проверяет, что *Error находится в цепочке через fmt-обёртку.
func TestFromErrorWrappedAppError(t *testing.T) {
	inner := New(KindForbidden, "no_access", "Доступ запрещён", "Ruxsat yo'q")
	wrapped := Wrap(inner, KindForbidden, "no_access", "Доступ запрещён", "Ruxsat yo'q")
	p := FromError(wrapped, "/x", "uz", "r1")
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.Equal(t, "Ruxsat yo'q", p.Detail)
}

// TestFromErrorInternalHidesDetails проверяет, что внутренняя ошибка
// не раскрывается клиенту.
func TestFromErrorInternalHidesDetails(t *testing.T) {
	secret := errors.New("pgx: connect failed: password=hunter2")

	tests := []struct {
		name       string
		lang       string
		wantDetail string
	}{
		{"ru", "ru", "Внутренняя ошибка сервера"},
		{"uz", "uz", "Ichki server xatosi"},
		{"other_lang", "en", "Внутренняя ошибка сервера"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := FromError(secret, "/api", tt.lang, "req-9")

			assert.Equal(t, http.StatusInternalServerError, p.Status)
			assert.Equal(t, http.StatusText(http.StatusInternalServerError), p.Title)
			assert.Equal(t, "about:blank", p.Type)
			assert.Equal(t, tt.wantDetail, p.Detail)
			assert.NotContains(t, p.Detail, "hunter2")
			assert.NotContains(t, p.Detail, "pgx")
			assert.Equal(t, "req-9", p.RequestID)
			assert.Empty(t, p.Errors)
		})
	}
}
