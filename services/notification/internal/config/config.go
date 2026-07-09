// Package config собирает конфигурацию Notification-сервиса из окружения.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"bozor/pkg/shared/config"
)

// dbName — база данных сервиса (создаётся init-скриптом compose).
const dbName = "bozor_notification"

const defaultAddr = ":8080"

// Значения по умолчанию для доставки и rate limiting Bot API.
const (
	defaultRatePerSec  = 25.0             // глобальный предел отправок/сек (лимиты Telegram Bot API ~30/сек)
	defaultRateBurst   = 25               // ёмкость bucket'а
	defaultPrefsTTL    = 30 * time.Second // TTL кеша настроек уведомлений
	defaultMaxDeliver  = 5                // попыток доставки события до DLQ
	defaultSendTimeout = 10 * time.Second // таймаут одного вызова Bot API
	defaultProfileURL  = "http://user-profile:8080"
)

// Config — конфигурация Notification-сервиса.
type Config struct {
	Addr     string
	Env      string
	LogLevel string

	// AppDSN — подключение рантайма через PgBouncer (transaction pooling).
	AppDSN string
	// MigrateDSN — прямое подключение к PostgreSQL для миграций (ADR-013).
	MigrateDSN string

	// NATSURL — адрес NATS JetStream (потребление доменных событий).
	NATSURL string
	// ProfileInternalURL — базовый URL внутреннего эндпоинта User/Profile
	// (чтение эффективных notification_prefs получателя).
	ProfileInternalURL string

	// TelegramBotToken — токен Bot API. Пустой => канал отключён (доставка
	// помечается skipped): удобно для локальной разработки без бота.
	TelegramBotToken string
	// TelegramAPIBase — базовый URL Bot API (переопределяется в тестах).
	TelegramAPIBase string
	// SendTimeout — таймаут одного вызова Bot API.
	SendTimeout time.Duration

	// RatePerSec / RateBurst — глобальный token-bucket отправок в Bot API.
	RatePerSec float64
	RateBurst  int
	// PrefsCacheTTL — TTL кеша настроек уведомлений.
	PrefsCacheTTL time.Duration
	// MaxDeliver — число попыток доставки события до отправки в DLQ.
	MaxDeliver int
}

// Load читает конфигурацию из окружения (fail-fast на обязательных ключах).
func Load() (*Config, error) {
	if missing := config.Missing("POSTGRES_USER", "POSTGRES_PASSWORD"); len(missing) > 0 {
		return nil, fmt.Errorf("config: не заданы обязательные переменные: %s", strings.Join(missing, ", "))
	}

	user := config.String("POSTGRES_USER", "")
	pass := config.String("POSTGRES_PASSWORD", "")

	return &Config{
		Addr:     config.String("NOTIFICATION_ADDR", defaultAddr),
		Env:      config.String("APP_ENV", "dev"),
		LogLevel: config.String("LOG_LEVEL", "info"),
		AppDSN: dsn(user, pass,
			config.String("PGBOUNCER_HOST", "pgbouncer"),
			config.String("PGBOUNCER_PORT", "6432")),
		MigrateDSN: dsn(user, pass,
			config.String("POSTGRES_HOST", "postgres"),
			config.String("POSTGRES_PORT", "5432")),
		NATSURL:            config.String("NATS_URL", "nats://nats:4222"),
		ProfileInternalURL: config.String("PROFILE_INTERNAL_URL", defaultProfileURL),
		TelegramBotToken:   config.String("TELEGRAM_BOT_TOKEN", ""),
		TelegramAPIBase:    config.String("TELEGRAM_API_BASE", "https://api.telegram.org"),
		SendTimeout:        config.Duration("NOTIFY_SEND_TIMEOUT", defaultSendTimeout),
		RatePerSec:         floatEnv("NOTIFY_RATE_PER_SEC", defaultRatePerSec),
		RateBurst:          config.Int("NOTIFY_RATE_BURST", defaultRateBurst),
		PrefsCacheTTL:      config.Duration("NOTIFY_PREFS_CACHE_TTL", defaultPrefsTTL),
		MaxDeliver:         config.Int("NOTIFY_MAX_DELIVER", defaultMaxDeliver),
	}, nil
}

// Addr возвращает адрес прослушивания сервиса (для сервера и self health-check).
func Addr() string {
	return config.String("NOTIFICATION_ADDR", defaultAddr)
}

// floatEnv читает float64 из окружения (config.String парсится в float);
// при отсутствии/ошибке — def.
func floatEnv(key string, def float64) float64 {
	v := config.String(key, "")
	if v == "" {
		return def
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%g", &f); err != nil || f <= 0 {
		return def
	}
	return f
}

// dsn собирает строку подключения к базе сервиса (bozor_notification).
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
