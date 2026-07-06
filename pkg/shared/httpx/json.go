package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"bozor/pkg/shared/apperr"
)

// Respond пишет v в ответ как JSON с указанным статусом и заголовком
// Content-Type: application/json; charset=utf-8. При v == nil тело
// не пишется — отправляется только статус.
func Respond(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// Ошибку кодирования обработать уже нельзя: заголовки отправлены.
	_ = json.NewEncoder(w).Encode(v)
}

// invalidJSON оборачивает причину err в доменную ошибку KindInvalid
// с кодом "invalid_json" и локализованными сообщениями.
func invalidJSON(err error) *apperr.Error {
	return apperr.Wrap(err, apperr.KindInvalid, "invalid_json",
		"Некорректный JSON в теле запроса", "So'rov tanasida noto'g'ri JSON")
}

// DecodeJSON читает тело запроса как JSON в dst.
//
// Размер тела ограничивается maxBytes через http.MaxBytesReader,
// неизвестные поля запрещены (DisallowUnknownFields), тело должно
// содержать ровно одно JSON-значение. Любая ошибка (пустое тело,
// синтаксис, лишние поля, второе значение, превышение размера)
// возвращается как *apperr.Error c KindInvalid и кодом "invalid_json".
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return invalidJSON(err)
	}

	// Проверяем, что после первого значения тело закончилось.
	var extra any
	switch err := dec.Decode(&extra); {
	case err == nil:
		return invalidJSON(errors.New("тело содержит более одного JSON-значения"))
	case !errors.Is(err, io.EOF):
		return invalidJSON(err)
	}
	return nil
}
