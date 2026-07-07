// Command server — точка входа API Gateway Bozor.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/logging"
	"bozor/pkg/shared/otelx"

	"bozor/services/gateway/internal/config"
	"bozor/services/gateway/internal/gateway"
	"bozor/services/gateway/internal/ratelimit"
)

const serviceName = "gateway"

func main() {
	healthcheck := flag.Bool("health", false, "выполнить self health-check по /healthz и выйти")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthCheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gateway:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logging.New(serviceName, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownOtel, err := otelx.Setup(ctx, serviceName)
	if err != nil {
		return fmt.Errorf("инициализация otel: %w", err)
	}
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownOtel(shCtx); err != nil {
			log.Error("остановка otel", slog.String("error", err.Error()))
		}
	}()

	metricsHandler, shutdownMetrics, err := otelx.SetupMetrics(serviceName)
	if err != nil {
		return fmt.Errorf("инициализация метрик: %w", err)
	}
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownMetrics(shCtx); err != nil {
			log.Error("остановка метрик", slog.String("error", err.Error()))
		}
	}()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	defer func() { _ = rdb.Close() }()

	router, err := gateway.NewRouter(gateway.Deps{
		Log:            log,
		JWTKey:         cfg.JWTSigningKey,
		Limiter:        ratelimit.NewRedisLimiter(rdb),
		RateRPS:        cfg.RateRPS,
		RateBurst:      cfg.RateBurst,
		AllowedOrigins: cfg.AllowedOrigins,
		Upstream:       config.Upstream,
		ReadyChecks: map[string]httpx.Check{
			"redis": func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
		},
		MetricsHandler: metricsHandler,
	})
	if err != nil {
		return err
	}

	log.Info("gateway стартует",
		slog.String("addr", cfg.Addr),
		slog.String("env", cfg.Env),
		slog.Int("rate_burst", cfg.RateBurst),
	)
	return httpx.Serve(ctx, cfg.Addr, router, log)
}

// runHealthCheck делает GET на локальный /healthz и возвращает код выхода:
// 0 при 200, иначе 1. Используется docker HEALTHCHECK (образ без shell).
func runHealthCheck() int {
	_, port, err := net.SplitHostPort(config.Addr())
	if err != nil || port == "" {
		port = "8080"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
