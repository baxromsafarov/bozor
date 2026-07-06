package httpx

import (
	"context"
	"net/http"
	"strings"
)

// langCtxKey — приватный тип ключа контекста для языка ответа.
type langCtxKey struct{}

// defaultLang — язык по умолчанию.
const defaultLang = "ru"

// Lang — middleware, определяющее язык ответа по заголовку Accept-Language:
// если один из языковых тегов начинается с "uz" — язык "uz", иначе "ru".
// Результат сохраняется в контекст запроса.
func Lang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := langFromHeader(r.Header.Get("Accept-Language"))
		ctx := context.WithValue(r.Context(), langCtxKey{}, lang)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// LangFromContext возвращает язык ответа из контекста
// или "ru", если язык не определён.
func LangFromContext(ctx context.Context) string {
	if lang, ok := ctx.Value(langCtxKey{}).(string); ok && lang != "" {
		return lang
	}
	return defaultLang
}

// langFromHeader разбирает значение Accept-Language: перебирает языковые
// теги через запятую (параметры качества ";q=..." отбрасываются) и при
// первом теге с префиксом "uz" возвращает "uz", иначе — "ru".
func langFromHeader(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag := part
		if i := strings.IndexByte(tag, ';'); i >= 0 {
			tag = tag[:i]
		}
		tag = strings.ToLower(strings.TrimSpace(tag))
		if strings.HasPrefix(tag, "uz") {
			return "uz"
		}
	}
	return defaultLang
}
