package httpx

import (
	"encoding/json"
	"net/http"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/logging"
)

// WriteProblem пишет ошибку err в ответ в формате RFC 7807
// (Content-Type: application/problem+json). Статус, тип и локализованное
// описание берутся из apperr.FromError; язык — из контекста запроса
// (LangFromContext), идентификатор запроса — из logging.RequestID.
func WriteProblem(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	p := apperr.FromError(err, r.URL.Path, LangFromContext(ctx), logging.RequestID(ctx))
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(p.Status)
	// Ошибку кодирования обработать уже нельзя: заголовки отправлены.
	_ = json.NewEncoder(w).Encode(p)
}
