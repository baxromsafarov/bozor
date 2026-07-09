// Command server — точка входа Notification-сервиса Bozor.
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
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/logging"
	"bozor/pkg/shared/migrate"
	"bozor/pkg/shared/natsx"
	"bozor/pkg/shared/otelx"
	"bozor/pkg/shared/pgxx"

	"bozor/services/notification/internal/config"
	"bozor/services/notification/internal/domain"
	"bozor/services/notification/internal/notify"
	"bozor/services/notification/internal/prefs"
	"bozor/services/notification/internal/repo"
	"bozor/services/notification/internal/telegram"
	"bozor/services/notification/internal/transport"
	"bozor/services/notification/migrations"
)

const serviceName = "notification"

// prefsTimeout — таймаут чтения настроек уведомлений из User/Profile.
const prefsTimeout = 5 * time.Second

func main() {
	healthcheck := flag.Bool("health", false, "выполнить self health-check по /healthz и выйти")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthCheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "notification:", err)
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

	store := repo.NewRepo(pool)

	// Зависимости доставки: канал Telegram, настройки уведомлений, rate limiter.
	sender := telegram.New(cfg.TelegramBotToken, cfg.TelegramAPIBase, cfg.SendTimeout)
	prefsClient := prefs.New(cfg.ProfileInternalURL, prefsTimeout, cfg.PrefsCacheTTL)
	limiter := rate.NewLimiter(rate.Limit(cfg.RatePerSec), cfg.RateBurst)
	channelEnabled := cfg.TelegramBotToken != ""
	if !channelEnabled {
		log.Warn("токен Telegram не задан — уведомления помечаются skipped(channel_disabled)")
	}

	// NATS JetStream: консьюмеры доставки и проекции получателей.
	nc, js, err := natsx.Connect(cfg.NATSURL, serviceName)
	if err != nil {
		return fmt.Errorf("подключение к NATS: %w", err)
	}
	defer nc.Close()
	if _, err := natsx.EnsureStream(ctx, js, events.StreamName, []string{events.SubjectsWildcard}); err != nil {
		return fmt.Errorf("создание стрима: %w", err)
	}

	// Консьюмер проекции получателей: bozor.user.created → recipients.
	recipients := notify.NewRecipients(store, log)
	ccRecipients, err := natsx.Consume(ctx, js, events.StreamName, notify.RecipientsConsumer,
		[]string{events.SubjectUserCreated}, cfg.MaxDeliver, recipients.Handle)
	if err != nil {
		return fmt.Errorf("консьюмер bozor.user.created: %w", err)
	}
	defer ccRecipients.Stop()

	// Консьюмер доставки: доменные события → рендер шаблона → Telegram.
	handler := notify.NewHandler(store, prefsClient, sender, limiter, channelEnabled, log)
	ccDelivery, err := natsx.Consume(ctx, js, events.StreamName, notify.DeliveryConsumer,
		domain.Subjects(), cfg.MaxDeliver, handler.Handle)
	if err != nil {
		return fmt.Errorf("консьюмер доставки уведомлений: %w", err)
	}
	defer ccDelivery.Stop()

	router := transport.NewRouter(transport.Deps{
		Log:            log,
		Handler:        transport.NewHandler(store, log),
		MetricsHandler: metricsHandler,
		ReadyChecks: map[string]httpx.Check{
			"postgres": pool.Ping,
			"nats": func(context.Context) error {
				if !nc.IsConnected() {
					return errors.New("nats: нет соединения")
				}
				return nil
			},
		},
	})

	log.Info("notification стартует", slog.String("addr", cfg.Addr), slog.String("env", cfg.Env))
	return httpx.Serve(ctx, cfg.Addr, router, log)
}

func shutdownWithTimeout(log *slog.Logger, name string, fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fn(ctx); err != nil {
		log.Error("остановка "+name, slog.String("error", err.Error()))
	}
}

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
