// Command server — точка входа Payments/Promotions-сервиса Bozor.
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

	"bozor/services/payments/internal/app"
	"bozor/services/payments/internal/config"
	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/listingclient"
	"bozor/services/payments/internal/provider"
	"bozor/services/payments/internal/provider/click"
	"bozor/services/payments/internal/provider/mock"
	"bozor/services/payments/internal/provider/payme"
	"bozor/services/payments/internal/repo"
	"bozor/services/payments/internal/transport"
	"bozor/services/payments/internal/worker"
	"bozor/services/payments/migrations"
)

const serviceName = "payments-promotions"

func main() {
	healthcheck := flag.Bool("health", false, "выполнить self health-check по /healthz и выйти")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthCheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "payments:", err)
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

	nc, js, err := natsx.Connect(cfg.NATSURL, serviceName)
	if err != nil {
		return fmt.Errorf("подключение к NATS: %w", err)
	}
	defer nc.Close()
	if _, err := natsx.EnsureStream(ctx, js, events.StreamName, []string{events.SubjectsWildcard}); err != nil {
		return fmt.Errorf("создание стрима: %w", err)
	}

	// Outbox relay: публикация bozor.payment.succeeded|failed в шину (→ Notification).
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

	// Провайдеры оплаты: mock (dev), Payme (JSON-RPC), Click (Prepare/Complete).
	mockProv := mock.New()
	paymeProv := payme.New(cfg.PaymeMerchantID, cfg.PaymeKey, app.NewProviderOps(store, domain.ProviderPayme), log)
	clickProv := click.New(cfg.ClickServiceID, cfg.ClickMerchantID, cfg.ClickSecretKey, app.NewProviderOps(store, domain.ProviderClick), log)
	paymentSvc := app.NewPaymentService(store, provider.NewRegistry(mockProv, paymeProv, clickProv), log)

	// Применение услуг к объявлениям (сага покупки): цена из каталога по региону/
	// категории объявления (читается из Listing), списание с кошелька, активация.
	listing := listingclient.New(cfg.ListingInternalURL, config.ListingTimeout)
	promotionSvc := app.NewPromotionService(listing, store, store, store, log)

	// Воркер авто-поднятий (Stage 8.5): по расписанию BUMP-услуг поднимает
	// объявления, дёргая внутренний эндпоинт Listing (он же публикует bozor.ad.bumped).
	bumper := worker.NewBumper(store, listing, cfg.BumpInterval, cfg.BumpBatch, log)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := bumper.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("воркер авто-поднятий остановлен с ошибкой", slog.String("error", err.Error()))
		}
	}()

	router := transport.NewRouter(transport.Deps{
		Log:            log,
		Catalog:        transport.NewCatalogHandler(app.NewService(store, log), log),
		Wallet:         transport.NewWalletHandler(app.NewWalletService(store, log), log),
		Payments:       transport.NewPaymentHandler(paymentSvc, log),
		Promotion:      transport.NewPromotionHandler(promotionSvc, log),
		PaymeCallback:  paymeProv,
		ClickCallback:  clickProv,
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

	log.Info("payments-promotions стартует", slog.String("addr", cfg.Addr), slog.String("env", cfg.Env))
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
