// Package config собирает конфигурацию User/Profile-сервиса из переменных окружения.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"bozor/pkg/shared/config"
)

// dbName — база данных сервиса (создаётся init-скриптом compose как bozor_profile).
const dbName = "bozor_profile"

const defaultAddr = ":8080"

// Config — конфигурация User/Profile-сервиса.
type Config struct {
	Addr     string
	Env      string
	LogLevel string

	// AppDSN — подключение рантайма через PgBouncer (transaction pooling).
	AppDSN string
	// MigrateDSN — прямое подключение к PostgreSQL для миграций (ADR-013).
	MigrateDSN string

	// NATSURL — адрес NATS JetStream (потребление bozor.user.created,
	// bozor.review.created; публикация bozor.user.updated из outbox).
	NATSURL string
	// ReviewsInternalURL — базовый URL внутреннего эндпоинта Reviews: агрегатор
	// рейтинга перечитывает актуальный рейтинг продавца (fetch-current-state).
	ReviewsInternalURL string
}

// Load читает конфигурацию из окружения (fail-fast на обязательных ключах).
func Load() (*Config, error) {
	if missing := config.Missing("POSTGRES_USER", "POSTGRES_PASSWORD"); len(missing) > 0 {
		return nil, fmt.Errorf("config: не заданы обязательные переменные: %s", strings.Join(missing, ", "))
	}

	user := config.String("POSTGRES_USER", "")
	pass := config.String("POSTGRES_PASSWORD", "")

	return &Config{
		Addr:     config.String("USER_PROFILE_ADDR", defaultAddr),
		Env:      config.String("APP_ENV", "dev"),
		LogLevel: config.String("LOG_LEVEL", "info"),
		AppDSN: dsn(user, pass,
			config.String("PGBOUNCER_HOST", "pgbouncer"),
			config.String("PGBOUNCER_PORT", "6432")),
		MigrateDSN: dsn(user, pass,
			config.String("POSTGRES_HOST", "postgres"),
			config.String("POSTGRES_PORT", "5432")),
		NATSURL:            config.String("NATS_URL", "nats://nats:4222"),
		ReviewsInternalURL: config.String("REVIEWS_INTERNAL_URL", "http://reviews:8080"),
	}, nil
}

// ReviewsTimeout — таймаут чтения агрегата рейтинга из Reviews.
const ReviewsTimeout = 5 * time.Second

// Addr возвращает адрес прослушивания сервиса (для сервера и self health-check).
func Addr() string {
	return config.String("USER_PROFILE_ADDR", defaultAddr)
}

// dsn собирает строку подключения к базе сервиса (bozor_profile) для host:port.
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
