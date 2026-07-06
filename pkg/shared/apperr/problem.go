package apperr

import (
	"errors"
	"net/http"
)

// problemTypeBase — базовый URI для типизированных проблем Bozor.
const problemTypeBase = "https://bozor.uz/problems/"

// FieldProblem — ошибка поля в ответе RFC 7807.
type FieldProblem struct {
	Field   string `json:"field"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// Problem — тело ответа об ошибке в формате RFC 7807 (application/problem+json).
type Problem struct {
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Status    int            `json:"status"`
	Detail    string         `json:"detail,omitempty"`
	Instance  string         `json:"instance,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Errors    []FieldProblem `json:"errors,omitempty"`
}

// FromError преобразует ошибку в Problem (RFC 7807).
//
// Для *Error: статус берётся из HTTPStatus, Title — каноническое название
// статуса, Detail — локализованное сообщение, Type — "about:blank" либо
// "https://bozor.uz/problems/<Code>" при непустом Code; Errors формируются
// из Fields с локализацией сообщений (fallback на русский).
//
// Для прочих ошибок возвращается 500 с общим локализованным сообщением —
// внутренние детали ошибки клиенту не раскрываются.
func FromError(err error, instance, lang, requestID string) Problem {
	var appErr *Error
	if !errors.As(err, &appErr) {
		detail := "Внутренняя ошибка сервера"
		if lang == "uz" {
			detail = "Ichki server xatosi"
		}
		return Problem{
			Type:      "about:blank",
			Title:     http.StatusText(http.StatusInternalServerError),
			Status:    http.StatusInternalServerError,
			Detail:    detail,
			Instance:  instance,
			RequestID: requestID,
		}
	}

	status := HTTPStatus(appErr.Kind)
	typ := "about:blank"
	if appErr.Code != "" {
		typ = problemTypeBase + appErr.Code
	}

	var fieldProblems []FieldProblem
	for _, f := range appErr.Fields {
		msg := f.MsgRU
		if lang == "uz" && f.MsgUZ != "" {
			msg = f.MsgUZ
		}
		fieldProblems = append(fieldProblems, FieldProblem{
			Field:   f.Field,
			Code:    f.Code,
			Message: msg,
		})
	}

	return Problem{
		Type:      typ,
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    appErr.Msg(lang),
		Instance:  instance,
		RequestID: requestID,
		Errors:    fieldProblems,
	}
}
