// Package config собирает конфигурацию Favorites/SavedSearch-сервиса из окружения.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"bozor/pkg/shared/config"
)

// dbName — база данных сервиса (создаётся init-скриптом compose).
const dbName = "bozor_favorites"

const defaultAddr = ":8080"

// defaultNotifyThrottle — окно троттлинга уведомлений на один сохранённый поиск
// (защита от лавины при массовом одобрении объявлений).
const defaultNotifyThrottle = time.Minute

// Config — конфигурация Favorites/SavedSearch-сервиса.
type Config struct {
	Addr     string
	Env      string
	LogLevel string

	// AppDSN — подключение рантайма через PgBouncer (transaction pooling).
	AppDSN string
	// MigrateDSN — прямое подключение к PostgreSQL для миграций (ADR-013).
	MigrateDSN string

	// NATSURL — адрес NATS JetStream (потребление bozor.ad.deleted — очистка
	// избранного; bozor.ad.approved — matcher сохранённых поисков).
	NATSURL string
	// ListingInternalURL — базовый URL внутреннего read-эндпоинта Listing
	// (matcher читает объявление для оценки фильтров).
	ListingInternalURL string
	// NotifyThrottle — окно троттлинга уведомлений на сохранённый поиск.
	NotifyThrottle time.Duration
}

// Load читает конфигурацию из окружения (fail-fast на обязательных ключах).
func Load() (*Config, error) {
	if missing := config.Missing("POSTGRES_USER", "POSTGRES_PASSWORD"); len(missing) > 0 {
		return nil, fmt.Errorf("config: не заданы обязательные переменные: %s", strings.Join(missing, ", "))
	}

	user := config.String("POSTGRES_USER", "")
	pass := config.String("POSTGRES_PASSWORD", "")

	return &Config{
		Addr:     config.String("FAVORITES_ADDR", defaultAddr),
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
		NotifyThrottle:     config.Duration("SAVED_SEARCH_NOTIFY_THROTTLE", defaultNotifyThrottle),
	}, nil
}

// Addr возвращает адрес прослушивания сервиса (для сервера и self health-check).
func Addr() string {
	return config.String("FAVORITES_ADDR", defaultAddr)
}

// dsn собирает строку подключения к базе сервиса (bozor_favorites) для host:port.
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
