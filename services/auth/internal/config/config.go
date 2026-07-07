// Package config собирает конфигурацию Auth-сервиса из переменных окружения.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"bozor/pkg/shared/config"
)

// dbName — база данных сервиса (создаётся init-скриптом compose).
const dbName = "bozor_auth"

const defaultAddr = ":8080"

// Значения по умолчанию для TTL токенов (переопределяются env).
const (
	defaultAccessTTL  = 15 * time.Minute
	defaultRefreshTTL = 720 * time.Hour // 30 дней
)

// Config — конфигурация Auth-сервиса.
type Config struct {
	Addr     string
	Env      string
	LogLevel string

	// AppDSN — подключение рантайма через PgBouncer (transaction pooling).
	AppDSN string
	// MigrateDSN — прямое подключение к PostgreSQL для миграций: сессионный
	// advisory-lock goose несовместим с transaction-пулингом (ADR-013).
	MigrateDSN string

	// NATSURL — адрес NATS JetStream для публикации доменных событий.
	NATSURL string

	// RedisAddr — адрес Redis для nonce'ов логина (host:port).
	RedisAddr string
	// RedisPassword — пароль Redis (пустой в dev).
	RedisPassword string

	// JWTSigningKey — секрет подписи access-JWT (HS256). Общий с gateway,
	// который проверяет токены тем же ключом.
	JWTSigningKey []byte
	// JWTAccessTTL — время жизни access-токена.
	JWTAccessTTL time.Duration
	// JWTRefreshTTL — время жизни refresh-токена.
	JWTRefreshTTL time.Duration

	// TelegramWebhookSecret — ожидаемое значение заголовка
	// X-Telegram-Bot-Api-Secret-Token во входящих вебхуках Telegram.
	TelegramWebhookSecret string
	// TelegramBotToken — токен бота для исходящих вызовов Bot API. Пустой —
	// отправка сообщений отключена (dev без реального бота).
	TelegramBotToken string
	// TelegramBotUsername — username бота для сборки deep-link
	// t.me/<username>?start=<nonce>. Пустой — ссылка не формируется (dev).
	TelegramBotUsername string
}

// Load читает конфигурацию из окружения (fail-fast на обязательных ключах).
func Load() (*Config, error) {
	if missing := config.Missing("POSTGRES_USER", "POSTGRES_PASSWORD", "TELEGRAM_WEBHOOK_SECRET", "JWT_SIGNING_KEY"); len(missing) > 0 {
		return nil, fmt.Errorf("config: не заданы обязательные переменные: %s", strings.Join(missing, ", "))
	}

	user := config.String("POSTGRES_USER", "")
	pass := config.String("POSTGRES_PASSWORD", "")

	return &Config{
		Addr:     config.String("AUTH_ADDR", defaultAddr),
		Env:      config.String("APP_ENV", "dev"),
		LogLevel: config.String("LOG_LEVEL", "info"),
		AppDSN: dsn(user, pass,
			config.String("PGBOUNCER_HOST", "pgbouncer"),
			config.String("PGBOUNCER_PORT", "6432")),
		MigrateDSN: dsn(user, pass,
			config.String("POSTGRES_HOST", "postgres"),
			config.String("POSTGRES_PORT", "5432")),
		NATSURL:               config.String("NATS_URL", "nats://nats:4222"),
		RedisAddr:             config.String("REDIS_ADDR", "redis:6379"),
		RedisPassword:         config.String("REDIS_PASSWORD", ""),
		JWTSigningKey:         []byte(config.String("JWT_SIGNING_KEY", "")),
		JWTAccessTTL:          config.Duration("JWT_ACCESS_TTL", defaultAccessTTL),
		JWTRefreshTTL:         config.Duration("JWT_REFRESH_TTL", defaultRefreshTTL),
		TelegramWebhookSecret: config.String("TELEGRAM_WEBHOOK_SECRET", ""),
		TelegramBotToken:      config.String("TELEGRAM_BOT_TOKEN", ""),
		TelegramBotUsername:   config.String("TELEGRAM_BOT_USERNAME", ""),
	}, nil
}

// Addr возвращает адрес прослушивания сервиса (для сервера и self health-check).
func Addr() string {
	return config.String("AUTH_ADDR", defaultAddr)
}

// dsn собирает строку подключения к базе сервиса (bozor_auth) для host:port.
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
