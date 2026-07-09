// Command server — точка входа Search-сервиса Bozor.
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

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/httpx"
	"bozor/pkg/shared/logging"
	"bozor/pkg/shared/natsx"
	"bozor/pkg/shared/otelx"

	"bozor/services/search/internal/config"
	"bozor/services/search/internal/indexer"
	"bozor/services/search/internal/listingclient"
	"bozor/services/search/internal/search"
	"bozor/services/search/internal/transport"
)

const serviceName = "search"

// Таймауты на запросы к зависимостям.
const (
	typesenseTimeout = 5 * time.Second
	listingTimeout   = 5 * time.Second
)

// indexerSubjects — события Listing/Moderation/Promotions, по которым индексатор
// пересобирает документ объявления (читая актуальное состояние из Listing).
var indexerSubjects = []string{
	events.SubjectAdCreated, events.SubjectAdUpdated, events.SubjectAdDeleted,
	events.SubjectAdApproved, events.SubjectAdBumped, events.SubjectAdSold, events.SubjectAdExpired,
}

func main() {
	healthcheck := flag.Bool("health", false, "выполнить self health-check по /healthz и выйти")
	reindex := flag.Bool("reindex", false, "полная переиндексация активных объявлений и выход")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthCheck())
	}
	if err := run(*reindex); err != nil {
		fmt.Fprintln(os.Stderr, "search:", err)
		os.Exit(1)
	}
}

func run(reindex bool) error {
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
	defer shutdownWithTimeout(log, "otel", shutdownOtel)

	metricsHandler, shutdownMetrics, err := otelx.SetupMetrics(serviceName)
	if err != nil {
		return fmt.Errorf("инициализация метрик: %w", err)
	}
	defer shutdownWithTimeout(log, "метрики", shutdownMetrics)

	client := search.New(cfg.TypesenseURL, cfg.TypesenseAPIKey, typesenseTimeout)

	// Создание коллекции ads при старте (идемпотентно).
	ensureCtx, ensureCancel := context.WithTimeout(ctx, 30*time.Second)
	created, err := client.EnsureCollection(ensureCtx, search.AdsSchema())
	ensureCancel()
	if err != nil {
		return fmt.Errorf("коллекция ads: %w", err)
	}
	log.Info("коллекция ads готова", slog.Bool("created", created))

	listing := listingclient.New(cfg.ListingInternalURL, listingTimeout)
	idx := indexer.New(client, listing, log)

	// Режим одноразовой переиндексации: перестроить индекс из экспорта и выйти.
	if reindex {
		n, err := idx.Reindex(ctx)
		if err != nil {
			return fmt.Errorf("переиндексация: %w", err)
		}
		log.Info("переиндексация выполнена", slog.Int("documents", n))
		return nil
	}

	// NATS JetStream: консьюмер индексатора (bozor.ad.* → Typesense).
	nc, js, err := natsx.Connect(cfg.NATSURL, serviceName)
	if err != nil {
		return fmt.Errorf("подключение к NATS: %w", err)
	}
	defer nc.Close()
	if _, err := natsx.EnsureStream(ctx, js, events.StreamName, []string{events.SubjectsWildcard}); err != nil {
		return fmt.Errorf("создание стрима: %w", err)
	}
	cc, err := natsx.Consume(ctx, js, events.StreamName, indexer.Consumer, indexerSubjects, 5, idx.Handle)
	if err != nil {
		return fmt.Errorf("консьюмер индексатора: %w", err)
	}
	defer cc.Stop()

	router := transport.NewRouter(transport.Deps{
		Log:            log,
		MetricsHandler: metricsHandler,
		ReadyChecks: map[string]httpx.Check{
			"typesense": client.Health,
			"nats": func(context.Context) error {
				if !nc.IsConnected() {
					return errors.New("nats: нет соединения")
				}
				return nil
			},
		},
	})

	log.Info("search стартует", slog.String("addr", cfg.Addr), slog.String("env", cfg.Env))
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
