// Package httpx содержит HTTP-утилиты Bozor: middleware (request id,
// восстановление после паник, access-лог, язык), JSON-хелперы, ответы
// об ошибках в формате RFC 7807, health/ready-хендлеры и запуск сервера
// с graceful shutdown.
package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/logging"
)

// headerRequestID — имя заголовка с идентификатором запроса.
const headerRequestID = "X-Request-Id"

// RequestID — middleware, которое берёт идентификатор запроса из заголовка
// X-Request-Id (или генерирует UUID v7, если заголовок пуст), сохраняет его
// в контекст через logging.WithRequestID и дублирует в заголовок ответа.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(headerRequestID, id)
		ctx := logging.WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// newRequestID генерирует идентификатор запроса: UUID v7, при сбое
// генератора — случайный UUID v4; в худшем случае — нулевой UUID.
// Функция никогда не паникует.
func newRequestID() string {
	id, err := uuid.NewV7()
	if err != nil {
		if fallback, err2 := uuid.NewRandom(); err2 == nil {
			id = fallback
		}
	}
	return id.String()
}

// Recover возвращает middleware, которое перехватывает панику обработчика,
// логирует её на уровне Error (с полем stack) и отвечает клиенту ошибкой
// 500 в формате RFC 7807. Паника http.ErrAbortHandler пробрасывается дальше,
// как того требует net/http.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				log.ErrorContext(r.Context(), "паника при обработке запроса",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)
				WriteProblem(w, r, apperr.New(apperr.KindInternal, "panic",
					"Внутренняя ошибка сервера", "Ichki server xatosi"))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog возвращает middleware, которое после обработки запроса пишет
// в лог уровня Info метод, путь, статус, размер ответа и длительность в мс.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(rw, r)
			log.InfoContext(r.Context(), "http запрос",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.Status()),
				slog.Int64("bytes", rw.bytes),
				slog.Float64("duration_ms", float64(time.Since(start))/float64(time.Millisecond)),
			)
		})
	}
}

// statusWriter — обёртка над http.ResponseWriter, запоминающая статус
// ответа и количество записанных байт.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

// WriteHeader запоминает статус и передаёт его исходному writer'у.
func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write учитывает записанные байты; при первом вызове без явного
// WriteHeader фиксирует статус 200.
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

// Status возвращает зафиксированный статус ответа (200, если обработчик
// ничего явно не записал).
func (w *statusWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Unwrap открывает исходный http.ResponseWriter для http.ResponseController.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
