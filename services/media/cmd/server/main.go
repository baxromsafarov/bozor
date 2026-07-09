// Command server — точка входа Media-сервиса Bozor.
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

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/logging"
	"bozor/pkg/shared/migrate"
	"bozor/pkg/shared/natsx"
	"bozor/pkg/shared/otelx"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/media/internal/app"
	"bozor/services/media/internal/config"
	"bozor/services/media/internal/domain"
	"bozor/services/media/internal/repo"
	"bozor/services/media/internal/storage"
	"bozor/services/media/internal/transport"
	"bozor/services/media/internal/worker"
	"bozor/services/media/migrations"
)

const serviceName = "media"

func main() {
	healthcheck := flag.Bool("health", false, "выполнить self health-check по /healthz и выйти")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthCheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "media:", err)
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

	blob, err := storage.New(storage.Config{
		Endpoint: cfg.MinIOEndpoint, AccessKey: cfg.MinIOAccessKey, SecretKey: cfg.MinIOSecretKey,
		UseSSL: cfg.MinIOUseSSL, Bucket: cfg.MediaBucket, PublicBaseURL: cfg.PublicBaseURL,
	})
	if err != nil {
		return fmt.Errorf("хранилище: %w", err)
	}
	bucketCtx, bucketCancel := context.WithTimeout(ctx, 10*time.Second)
	err = blob.EnsureBucket(bucketCtx)
	bucketCancel()
	if err != nil {
		return fmt.Errorf("бакет: %w", err)
	}

	// NATS JetStream + relay: события bozor.media.* из outbox в шину.
	nc, js, err := natsx.Connect(cfg.NATSURL, serviceName)
	if err != nil {
		return fmt.Errorf("подключение к NATS: %w", err)
	}
	defer nc.Close()
	if _, err := natsx.EnsureStream(ctx, js, events.StreamName, []string{events.SubjectsWildcard}); err != nil {
		return fmt.Errorf("создание стрима: %w", err)
	}

	mediaRepo := repo.NewRepo(pool)

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

	// Воркер обработки: потребляет bozor.media.uploaded, генерирует превью
	// и снимает EXIF (Stage 3.2). До 3 попыток, затем DLQ (natsx).
	processor := worker.NewProcessor(mediaRepo, blob, log)
	cc, err := natsx.Consume(ctx, js, events.StreamName, worker.Consumer,
		[]string{events.SubjectMediaUploaded}, 3, processor.Handle)
	if err != nil {
		return fmt.Errorf("консьюмер обработки медиа: %w", err)
	}
	defer cc.Stop()

	// Периодическая очистка сирот (медиа без объявления старше TTL).
	cleaner := worker.NewCleaner(mediaRepo, blob, cfg.OrphanTTL, cfg.CleanInterval, cfg.CleanBatchSize, log)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := cleaner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("очистка сирот остановлена с ошибкой", slog.String("error", err.Error()))
		}
	}()

	limits := domain.Limits{MaxSizeBytes: cfg.MaxSizeBytes, MaxPerAd: cfg.MaxPerAd}
	svc := app.NewService(mediaRepo, blob, limits, cfg.PresignTTL, log)
	handler := transport.NewHandler(svc, cfg.MaxSizeBytes, log)

	router := transport.NewRouter(transport.Deps{
		Log:            log,
		Handler:        handler,
		MetricsHandler: metricsHandler,
		ReadyChecks: map[string]httpx.Check{
			"postgres": pool.Ping,
			"minio":    blob.Ping,
			"nats": func(context.Context) error {
				if !nc.IsConnected() {
					return errors.New("nats: нет соединения")
				}
				return nil
			},
		},
	})

	log.Info("media стартует", slog.String("addr", cfg.Addr), slog.String("env", cfg.Env))
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
