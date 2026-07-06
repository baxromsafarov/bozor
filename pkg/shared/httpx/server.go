package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// shutdownTimeout — таймаут на graceful shutdown сервера.
const shutdownTimeout = 15 * time.Second

// Serve запускает HTTP-сервер на addr с обработчиком h и безопасными
// таймаутами (ReadHeaderTimeout 5s, ReadTimeout 30s, WriteTimeout 60s,
// IdleTimeout 120s). Сервер работает в отдельной горутине; при отмене ctx
// выполняется graceful shutdown с таймаутом 15 секунд. Возврат
// http.ErrServerClosed ошибкой не считается.
func Serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http сервер запущен", slog.String("addr", addr))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("httpx: сервер %s: %w", addr, err)
		}
		return nil
	case <-ctx.Done():
		log.Info("останавливаем http сервер", slog.String("addr", addr))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("httpx: остановка сервера %s: %w", addr, err)
		}
		// Дожидаемся завершения горутины ListenAndServe.
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("httpx: сервер %s: %w", addr, err)
		}
		return nil
	}
}
