// Package apperr содержит доменные ошибки приложения Bozor с поддержкой
// локализации (ru/uz) и преобразования в HTTP-ответы формата RFC 7807.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind — категория доменной ошибки. Определяет HTTP-статус ответа.
type Kind int

// Категории ошибок.
const (
	// KindInvalid — ошибка валидации входных данных.
	KindInvalid Kind = iota
	// KindNotFound — ресурс не найден.
	KindNotFound
	// KindUnauthorized — требуется аутентификация.
	KindUnauthorized
	// KindForbidden — доступ запрещён.
	KindForbidden
	// KindConflict — конфликт состояния (например, дубликат).
	KindConflict
	// KindRateLimited — превышен лимит запросов.
	KindRateLimited
	// KindUnavailable — сервис временно недоступен.
	KindUnavailable
	// KindInternal — внутренняя ошибка сервера.
	KindInternal
)

// FieldError — ошибка валидации конкретного поля с локализованными сообщениями.
type FieldError struct {
	Field string // имя поля
	Code  string // машиночитаемый код ошибки
	MsgRU string // сообщение на русском
	MsgUZ string // сообщение на узбекском
}

// Error — доменная ошибка с категорией, кодом, локализованными сообщениями,
// ошибками полей и вложенной причиной.
type Error struct {
	Kind   Kind         // категория ошибки
	Code   string       // машиночитаемый код (например, "product_not_found")
	MsgRU  string       // сообщение на русском
	MsgUZ  string       // сообщение на узбекском
	Fields []FieldError // ошибки отдельных полей (для валидации)
	Err    error        // вложенная причина
}

// Error реализует интерфейс error: возвращает код, русское сообщение
// и вложенную ошибку (если есть).
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.MsgRU, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.MsgRU)
}

// Unwrap возвращает вложенную ошибку для работы errors.Is/errors.As.
func (e *Error) Unwrap() error { return e.Err }

// Msg возвращает локализованное сообщение: для lang == "uz" — MsgUZ
// (с fallback на MsgRU, если узбекский перевод пуст), иначе — MsgRU.
func (e *Error) Msg(lang string) string {
	if lang == "uz" && e.MsgUZ != "" {
		return e.MsgUZ
	}
	return e.MsgRU
}

// New создаёт новую доменную ошибку без вложенной причины.
func New(kind Kind, code, msgRU, msgUZ string) *Error {
	return &Error{Kind: kind, Code: code, MsgRU: msgRU, MsgUZ: msgUZ}
}

// Wrap оборачивает ошибку err в доменную ошибку. Исходная ошибка
// доступна через Unwrap и errors.Is/errors.As.
func Wrap(err error, kind Kind, code, msgRU, msgUZ string) *Error {
	return &Error{Kind: kind, Code: code, MsgRU: msgRU, MsgUZ: msgUZ, Err: err}
}

// WithFields добавляет ошибки полей и возвращает тот же *Error
// для цепочечного вызова.
func (e *Error) WithFields(fields ...FieldError) *Error {
	e.Fields = append(e.Fields, fields...)
	return e
}

// KindOf возвращает категорию ошибки: если в цепочке err найден *Error —
// его Kind, иначе KindInternal.
func KindOf(err error) Kind {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	return KindInternal
}

// HTTPStatus сопоставляет категорию ошибки с HTTP-статусом.
func HTTPStatus(k Kind) int {
	switch k {
	case KindInvalid:
		return http.StatusUnprocessableEntity
	case KindNotFound:
		return http.StatusNotFound
	case KindUnauthorized:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindConflict:
		return http.StatusConflict
	case KindRateLimited:
		return http.StatusTooManyRequests
	case KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
