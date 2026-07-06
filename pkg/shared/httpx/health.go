package httpx

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// readyTimeout — общий таймаут на выполнение всех проверок готовности.
const readyTimeout = 5 * time.Second

// Check — проверка готовности зависимости (БД, брокер и т.п.).
// Возвращает nil, если зависимость доступна.
type Check func(ctx context.Context) error

// HealthHandler возвращает обработчик liveness-проверки:
// всегда отвечает 200 {"status":"ok"}.
func HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Respond(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// ReadyHandler возвращает обработчик readiness-проверки. Все проверки
// выполняются параллельно с общим таймаутом 5 секунд. Если все прошли —
// ответ 200 {"status":"ready"}; иначе 503 {"status":"unavailable",
// "checks":{"<имя>":"ok"|"<текст ошибки>"}}.
func ReadyHandler(checks map[string]Check) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		var (
			mu      sync.Mutex
			results = make(map[string]string, len(checks))
			failed  bool
			wg      sync.WaitGroup
		)
		for name, check := range checks {
			wg.Add(1)
			go func(name string, check Check) {
				defer wg.Done()
				status := "ok"
				if err := check(ctx); err != nil {
					status = err.Error()
				}
				mu.Lock()
				defer mu.Unlock()
				results[name] = status
				if status != "ok" {
					failed = true
				}
			}(name, check)
		}
		wg.Wait()

		if !failed {
			Respond(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
		Respond(w, http.StatusServiceUnavailable, map[string]any{
			"status": "unavailable",
			"checks": results,
		})
	}
}
