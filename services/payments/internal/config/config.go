// Package config собирает конфигурацию Payments/Promotions-сервиса из окружения.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"bozor/pkg/shared/config"
)

// dbName — база данных сервиса (создаётся init-скриптом compose).
const dbName = "bozor_payments"

const defaultAddr = ":8080"

// Параметры воркера авто-поднятий (Stage 8.5).
const (
	defaultBumpInterval = 1 * time.Minute // период проверки созревших поднятий
	defaultBumpBatch    = 100             // сколько поднятий исполняется за проход
)

// Config — конфигурация Payments/Promotions-сервиса.
type Config struct {
	Addr     string
	Env      string
	LogLevel string

	// AppDSN — подключение рантайма через PgBouncer (transaction pooling).
	AppDSN string
	// MigrateDSN — прямое подключение к PostgreSQL для миграций (ADR-013).
	MigrateDSN string

	// NATSURL — адрес NATS JetStream (публикация bozor.payment.succeeded|failed).
	NATSURL string

	// ListingInternalURL — базовый URL внутреннего read-эндпоинта Listing
	// (при покупке услуги читается объявление: владелец, регион/категория, статус).
	ListingInternalURL string

	// Параметры воркера авто-поднятий (Stage 8.5).
	BumpInterval time.Duration // период проверки созревших поднятий
	BumpBatch    int           // размер пакета исполняемых поднятий за проход

	// Реквизиты провайдеров оплаты (для проверки колбэков и ссылок оплаты).
	PaymeMerchantID string
	PaymeKey        string
	ClickServiceID  string
	ClickMerchantID string
	ClickSecretKey  string
}

// Load читает конфигурацию из окружения (fail-fast на обязательных ключах).
func Load() (*Config, error) {
	if missing := config.Missing("POSTGRES_USER", "POSTGRES_PASSWORD"); len(missing) > 0 {
		return nil, fmt.Errorf("config: не заданы обязательные переменные: %s", strings.Join(missing, ", "))
	}

	user := config.String("POSTGRES_USER", "")
	pass := config.String("POSTGRES_PASSWORD", "")

	return &Config{
		Addr:     config.String("PAYMENTS_ADDR", defaultAddr),
		Env:      config.String("APP_ENV", "dev"),
		LogLevel: config.String("LOG_LEVEL", "info"),
		AppDSN: dsn(user, pass,
			config.String("PGBOUNCER_HOST", "pgbouncer"),
			config.String("PGBOUNCER_PORT", "6432")),
		MigrateDSN: dsn(user, pass,
			config.String("POSTGRES_HOST", "postgres"),
			config.String("POSTGRES_PORT", "5432")),
		NATSURL:            config.String("NATS_URL", "nats://nats:4222"),
		ListingInternalURL: config.String("LISTING_INTERNAL_URL", "http://listing-ads:8080"),
		BumpInterval:       config.Duration("PAYMENTS_BUMP_INTERVAL", defaultBumpInterval),
		BumpBatch:          config.Int("PAYMENTS_BUMP_BATCH", defaultBumpBatch),
		PaymeMerchantID:    config.String("PAYME_MERCHANT_ID", "payme-merchant-dev"),
		PaymeKey:           config.String("PAYME_KEY", "payme-key-dev"),
		ClickServiceID:     config.String("CLICK_SERVICE_ID", "click-service-dev"),
		ClickMerchantID:    config.String("CLICK_MERCHANT_ID", "click-merchant-dev"),
		ClickSecretKey:     config.String("CLICK_SECRET_KEY", "click-secret-dev"),
	}, nil
}

// Addr возвращает адрес прослушивания сервиса (для сервера и self health-check).
func Addr() string {
	return config.String("PAYMENTS_ADDR", defaultAddr)
}

// ListingTimeout — таймаут чтения объявления из Listing при покупке услуги.
const ListingTimeout = 5 * time.Second

// dsn собирает строку подключения к базе сервиса (bozor_payments).
func dsn(user, pass, host, port string) string {
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, pass),
		Host:     host + ":" + port,
		Path:     "/" + dbName,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}
