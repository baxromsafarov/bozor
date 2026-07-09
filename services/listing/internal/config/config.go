// Package config собирает конфигурацию Listing-сервиса из переменных окружения.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"bozor/pkg/shared/config"
)

// dbName — база данных сервиса (создаётся init-скриптом compose).
const dbName = "bozor_listing"

const defaultAddr = ":8080"

// Значения по умолчанию для жизненного цикла объявлений (Stage 3.4).
const (
	defaultAdTTL          = 30 * 24 * time.Hour // срок активного объявления (продлевается renew)
	defaultExpireInterval = 5 * time.Minute     // период воркера истечения
	defaultExpireBatch    = 100                 // сколько объявлений истекает за проход
)

// Config — конфигурация Listing-сервиса.
type Config struct {
	Addr     string
	Env      string
	LogLevel string

	// AppDSN — подключение рантайма через PgBouncer (transaction pooling).
	AppDSN string
	// MigrateDSN — прямое подключение к PostgreSQL для миграций (ADR-013).
	MigrateDSN string

	// NATSURL — адрес NATS JetStream для публикации событий bozor.ad.*.
	NATSURL string

	// CatalogGRPCAddr — адрес gRPC Catalog для валидации атрибутов.
	CatalogGRPCAddr string

	// Жизненный цикл объявлений (Stage 3.4).
	AdTTL          time.Duration // срок активного объявления
	ExpireInterval time.Duration // период воркера истечения
	ExpireBatch    int           // размер пакета истечения
}

// Load читает конфигурацию из окружения (fail-fast на обязательных ключах).
func Load() (*Config, error) {
	if missing := config.Missing("POSTGRES_USER", "POSTGRES_PASSWORD"); len(missing) > 0 {
		return nil, fmt.Errorf("config: не заданы обязательные переменные: %s", strings.Join(missing, ", "))
	}

	user := config.String("POSTGRES_USER", "")
	pass := config.String("POSTGRES_PASSWORD", "")

	return &Config{
		Addr:     config.String("LISTING_ADDR", defaultAddr),
		Env:      config.String("APP_ENV", "dev"),
		LogLevel: config.String("LOG_LEVEL", "info"),
		AppDSN: dsn(user, pass,
			config.String("PGBOUNCER_HOST", "pgbouncer"),
			config.String("PGBOUNCER_PORT", "6432")),
		MigrateDSN: dsn(user, pass,
			config.String("POSTGRES_HOST", "postgres"),
			config.String("POSTGRES_PORT", "5432")),
		NATSURL:         config.String("NATS_URL", "nats://nats:4222"),
		CatalogGRPCAddr: config.String("CATALOG_GRPC_ADDR", "catalog:9090"),
		AdTTL:           config.Duration("LISTING_AD_TTL", defaultAdTTL),
		ExpireInterval:  config.Duration("LISTING_EXPIRE_INTERVAL", defaultExpireInterval),
		ExpireBatch:     config.Int("LISTING_EXPIRE_BATCH", defaultExpireBatch),
	}, nil
}

// Addr возвращает адрес прослушивания сервиса (для сервера и self health-check).
func Addr() string {
	return config.String("LISTING_ADDR", defaultAddr)
}

// dsn собирает строку подключения к базе сервиса (bozor_listing) для host:port.
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
