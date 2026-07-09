// Command server — точка входа Listing-сервиса Bozor.
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

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/grpcx"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/logging"
	"bozor/pkg/shared/migrate"
	"bozor/pkg/shared/natsx"
	"bozor/pkg/shared/otelx"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/listing/internal/app"
	"bozor/services/listing/internal/catalogclient"
	"bozor/services/listing/internal/config"
	"bozor/services/listing/internal/repo"
	"bozor/services/listing/internal/transport"
	"bozor/services/listing/internal/views"
	"bozor/services/listing/internal/worker"
	"bozor/services/listing/migrations"
)

const serviceName = "listing"

func main() {
	healthcheck := flag.Bool("health", false, "выполнить self health-check по /healthz и выйти")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthCheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "listing:", err)
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

	// gRPC-клиент Catalog для валидации атрибутов (соединение ленивое).
	catalogConn, err := grpcx.Dial(cfg.CatalogGRPCAddr)
	if err != nil {
		return fmt.Errorf("gRPC Catalog: %w", err)
	}
	defer func() { _ = catalogConn.Close() }()

	// Redis: буфер счётчика просмотров (Stage 3.5).
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	defer func() { _ = rdb.Close() }()
	viewCounter := views.NewCounter(rdb)

	// NATS JetStream + relay: события bozor.ad.* из outbox в шину.
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
	defer wg.Wait()

	adRepo := repo.NewRepo(pool)

	// Консьюмер решений модерации: bozor.ad.approved|rejected → active|rejected
	// (Stage 3.4). До 3 попыток, затем DLQ (natsx); идемпотентность через inbox.
	moderator := worker.NewModerator(adRepo, cfg.AdTTL, log)
	cc, err := natsx.Consume(ctx, js, events.StreamName, worker.ModerationConsumer,
		[]string{events.SubjectAdApproved, events.SubjectAdRejected, events.SubjectAdBlocked}, 3, moderator.Handle)
	if err != nil {
		return fmt.Errorf("консьюмер решений модерации: %w", err)
	}
	defer cc.Stop()

	// Воркер истечения срока: active → expired по expires_at, публикует bozor.ad.expired.
	expirer := worker.NewExpirer(adRepo, cfg.ExpireInterval, cfg.ExpireBatch, log)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := expirer.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("воркер истечения остановлен с ошибкой", slog.String("error", err.Error()))
		}
	}()

	// Воркер флеша просмотров: буфер Redis → ads.views_count пачкой (Stage 3.5).
	viewFlusher := worker.NewViewFlusher(adRepo, viewCounter, cfg.ViewsFlushInterval, log)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := viewFlusher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("воркер флеша просмотров остановлен с ошибкой", slog.String("error", err.Error()))
		}
	}()

	svc := app.NewService(adRepo, catalogclient.New(catalogConn), viewCounter, cfg.AdTTL, log)
	handler := transport.NewHandler(svc, log)

	router := transport.NewRouter(transport.Deps{
		Log:            log,
		Handler:        handler,
		MetricsHandler: metricsHandler,
		ReadyChecks: map[string]httpx.Check{
			"postgres": pool.Ping,
			"redis":    func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
			"nats": func(context.Context) error {
				if !nc.IsConnected() {
					return errors.New("nats: нет соединения")
				}
				return nil
			},
		},
	})

	log.Info("listing стартует", slog.String("addr", cfg.Addr), slog.String("env", cfg.Env))
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
