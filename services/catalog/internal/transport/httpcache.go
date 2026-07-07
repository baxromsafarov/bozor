package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// publicCacheControl — политика кеширования публичных ответов каталога.
// Тело меняется редко (правки админом) и защищено ETag'ом; короткий max-age
// снижает нагрузку, ETag даёт условные запросы (304) для клиентов и прокси.
const publicCacheControl = "public, max-age=60"

// writeCachedJSON пишет уже сериализованное JSON-тело с ETag и Cache-Control.
// При совпадении If-None-Match отвечает 304 без тела.
func writeCachedJSON(w http.ResponseWriter, r *http.Request, body []byte) {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", publicCacheControl)

	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// body — сериализованный сервером JSON, Content-Type application/json (не HTML):
	// XSS-вектора нет, taint-анализ gosec срабатывает ложно на границе функции.
	_, _ = w.Write(body) //nolint:gosec // G705: не HTML-sink, тело — серверный JSON
}

// etagMatches проверяет заголовок If-None-Match на совпадение с ETag.
// Поддерживает "*" и список тегов через запятую.
func etagMatches(header, etag string) bool {
	if header == "*" {
		return true
	}
	for len(header) > 0 {
		// пропускаем ведущие пробелы и запятые
		i := 0
		for i < len(header) && (header[i] == ' ' || header[i] == ',') {
			i++
		}
		header = header[i:]
		if header == "" {
			break
		}
		// вырезаем один тег до следующей запятой
		j := 0
		for j < len(header) && header[j] != ',' {
			j++
		}
		tag := header[:j]
		header = header[j:]
		// снимаем слабый префикс W/
		if len(tag) >= 2 && tag[0] == 'W' && tag[1] == '/' {
			tag = tag[2:]
		}
		if tag == etag {
			return true
		}
	}
	return false
}
