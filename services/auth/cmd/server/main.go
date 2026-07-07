// Command server — точка входа Auth-сервиса Bozor.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"bozor/pkg/shared/authx"
	"bozor/pkg/shared/events"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/logging"
	"bozor/pkg/shared/migrate"
	"bozor/pkg/shared/natsx"
	"bozor/pkg/shared/otelx"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/auth/internal/app"
	"bozor/services/auth/internal/config"
	"bozor/services/auth/internal/ratelimit"
	"bozor/services/auth/internal/repo"
	"bozor/services/auth/internal/session"
	"bozor/services/auth/internal/telegram"
	"bozor/services/auth/internal/transport"
	"bozor/services/auth/migrations"
)

const serviceName = "auth"

// Лимиты частоты (запросов в минуту на IP) для публичных эндпоинтов.
const (
	initRateLimit    = 10  // инициация логина
	webhookRateLimit = 120 // доставки вебхука Telegram
)

func main() {
	healthcheck := flag.Bool("health", false, "выполнить self health-check по /healthz и выйти")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthCheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "auth:", err)
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

	// Миграции применяются при старте по прямому DSN (не через PgBouncer).
	migCtx, migCancel := context.WithTimeout(ctx, 60*time.Second)
	applied, err := migrate.Up(migCtx, cfg.MigrateDSN, migrations.FS)
	migCancel()
	if err != nil {
		return fmt.Errorf("миграции: %w", err)
	}
	log.Info("миграции применены", slog.Int("applied", applied))

	shutdownOtel, err := otelx.Setup(ctx, serviceName)
	if err != nil {
		return fmt.Errorf("инициализация otel: %w", err)
	}
	defer shutdownWithTimeout(log, "otel", shutdownOtel)

	metricsHandler, shutdownMetrics, err := otelx.SetupMetrics(serviceName)
	if err != nil {
		return fmt.Errorf("инициализация метрик: %w", err)
	}
	defer shutdownWithTimeout(log, "метрики", shutdownMetrics)

	pool, err := pgxx.NewPool(ctx, cfg.AppDSN)
	if err != nil {
		return fmt.Errorf("пул БД: %w", err)
	}
	defer pool.Close()

	// Redis: одноразовые nonce'ы логина (deep-link сессии).
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	defer func() { _ = rdb.Close() }()
	sessions := session.NewStore(rdb)

	// NATS JetStream + relay: события из outbox публикуются в шину.
	nc, js, err := natsx.Connect(cfg.NATSURL, serviceName)
	if err != nil {
		return fmt.Errorf("подключение к NATS: %w", err)
	}
	defer nc.Close()
	if _, err := natsx.EnsureStream(ctx, js, events.StreamName, []string{events.SubjectsWildcard}); err != nil {
		return fmt.Errorf("создание стрима: %w", err)
	}

	relay := &outbox.Relay{
		Pool:    pool,
		Publish: func(ctx context.Context, env events.Envelope) error { return natsx.Publish(ctx, js, env) },
		Log:     log,
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := relay.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("outbox relay остановлен с ошибкой", slog.String("error", err.Error()))
		}
	}()
	defer wg.Wait() // дождаться завершения relay до nc.Close (defer LIFO)

	userRepo := repo.NewUserRepo(pool)
	refreshRepo := repo.NewRefreshRepo(pool)
	auditRepo := repo.NewAuditRepo(pool)
	signer := authx.NewSigner(cfg.JWTSigningKey, serviceName, cfg.JWTAccessTTL)
	tokenSvc := app.NewTokenService(signer, refreshRepo, auditRepo, cfg.JWTRefreshTTL, log)

	limiter := ratelimit.New(rdb)
	svc := app.NewService(userRepo)
	bot := telegram.New(cfg.TelegramBotToken)
	webhook := transport.NewWebhookHandler(cfg.TelegramWebhookSecret, svc, bot, sessions, log)
	sessionHandler := transport.NewSessionHandler(sessions, tokenSvc, cfg.TelegramBotUsername, log)
	tokenHandler := transport.NewTokenHandler(tokenSvc, log)
	meHandler := transport.NewMeHandler()

	router := transport.NewRouter(transport.Deps{
		Log:            log,
		Webhook:        webhook,
		Session:        sessionHandler,
		Token:          tokenHandler,
		Me:             meHandler,
		MetricsHandler: metricsHandler,
		InitRateLimit: ratelimit.Middleware(limiter, "init",
			initRateLimit, time.Minute, ratelimit.ClientIPKey, log),
		WebhookRateLimit: ratelimit.Middleware(limiter, "webhook",
			webhookRateLimit, time.Minute, ratelimit.ClientIPKey, log),
		ReadyChecks: map[string]httpx.Check{
			"postgres": pool.Ping,
			"nats": func(context.Context) error {
				if !nc.IsConnected() {
					return errors.New("nats: нет соединения")
				}
				return nil
			},
			"redis": func(ctx context.Context) error {
				return rdb.Ping(ctx).Err()
			},
		},
	})

	log.Info("auth стартует", slog.String("addr", cfg.Addr), slog.String("env", cfg.Env))
	return httpx.Serve(ctx, cfg.Addr, router, log)
}

// shutdownWithTimeout вызывает fn с ограничением по времени и логирует ошибку.
func shutdownWithTimeout(log *slog.Logger, name string, fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fn(ctx); err != nil {
		log.Error("остановка "+name, slog.String("error", err.Error()))
	}
}

// runHealthCheck делает GET на локальный /healthz (docker HEALTHCHECK без shell).
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
