// Package config собирает конфигурацию Media-сервиса из переменных окружения.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"bozor/pkg/shared/config"
)

// dbName — база данных сервиса (создаётся init-скриптом compose).
const dbName = "bozor_media"

const defaultAddr = ":8080"

// Значения по умолчанию для лимитов загрузки.
const (
	defaultMaxSizeBytes = 10 << 20 // 10 MiB на файл
	defaultMaxPerAd     = 10       // максимум изображений на объявление
)

// Значения по умолчанию для обработки и очистки медиа (Stage 3.2).
const (
	defaultOrphanTTL      = 24 * time.Hour   // возраст непривязанного медиа до очистки
	defaultCleanInterval  = 15 * time.Minute // период запуска очистки сирот
	defaultPresignTTL     = 15 * time.Minute // срок жизни presigned-ссылки на оригинал
	defaultCleanBatchSize = 100              // сколько сирот удалять за один проход
)

// Config — конфигурация Media-сервиса.
type Config struct {
	Addr     string
	Env      string
	LogLevel string

	// AppDSN — подключение рантайма через PgBouncer (transaction pooling).
	AppDSN string
	// MigrateDSN — прямое подключение к PostgreSQL для миграций (ADR-013).
	MigrateDSN string

	// NATSURL — адрес NATS JetStream для публикации событий bozor.media.*.
	NATSURL string

	// MinIO / S3-хранилище оригиналов.
	MinIOEndpoint  string
	MinIOAccessKey string
	MinIOSecretKey string
	MinIOUseSSL    bool
	MediaBucket    string
	PublicBaseURL  string

	// Лимиты загрузки.
	MaxSizeBytes int64
	MaxPerAd     int

	// Обработка и очистка медиа (Stage 3.2).
	OrphanTTL      time.Duration // непривязанное медиа старше TTL считается сиротой
	CleanInterval  time.Duration // период запуска очистки сирот
	CleanBatchSize int           // размер пакета очистки за один проход
	PresignTTL     time.Duration // срок жизни presigned-ссылки на оригинал
}

// Load читает конфигурацию из окружения (fail-fast на обязательных ключах).
func Load() (*Config, error) {
	required := []string{"POSTGRES_USER", "POSTGRES_PASSWORD", "MINIO_ROOT_USER", "MINIO_ROOT_PASSWORD"}
	if missing := config.Missing(required...); len(missing) > 0 {
		return nil, fmt.Errorf("config: не заданы обязательные переменные: %s", strings.Join(missing, ", "))
	}

	user := config.String("POSTGRES_USER", "")
	pass := config.String("POSTGRES_PASSWORD", "")

	return &Config{
		Addr:     config.String("MEDIA_ADDR", defaultAddr),
		Env:      config.String("APP_ENV", "dev"),
		LogLevel: config.String("LOG_LEVEL", "info"),
		AppDSN: dsn(user, pass,
			config.String("PGBOUNCER_HOST", "pgbouncer"),
			config.String("PGBOUNCER_PORT", "6432")),
		MigrateDSN: dsn(user, pass,
			config.String("POSTGRES_HOST", "postgres"),
			config.String("POSTGRES_PORT", "5432")),
		NATSURL:        config.String("NATS_URL", "nats://nats:4222"),
		MinIOEndpoint:  config.String("MINIO_ENDPOINT", "minio:9000"),
		MinIOAccessKey: config.String("MINIO_ROOT_USER", ""),
		MinIOSecretKey: config.String("MINIO_ROOT_PASSWORD", ""),
		MinIOUseSSL:    config.Bool("MINIO_USE_SSL", false),
		MediaBucket:    config.String("MEDIA_BUCKET", "bozor-media"),
		PublicBaseURL:  strings.TrimRight(config.String("MEDIA_PUBLIC_BASE_URL", "http://localhost:9000/bozor-media"), "/"),
		MaxSizeBytes:   int64(config.Int("MEDIA_MAX_SIZE_BYTES", defaultMaxSizeBytes)),
		MaxPerAd:       config.Int("MEDIA_MAX_PER_AD", defaultMaxPerAd),
		OrphanTTL:      config.Duration("MEDIA_ORPHAN_TTL", defaultOrphanTTL),
		CleanInterval:  config.Duration("MEDIA_CLEAN_INTERVAL", defaultCleanInterval),
		CleanBatchSize: config.Int("MEDIA_CLEAN_BATCH_SIZE", defaultCleanBatchSize),
		PresignTTL:     config.Duration("MEDIA_PRESIGN_TTL", defaultPresignTTL),
	}, nil
}

// Addr возвращает адрес прослушивания сервиса (для сервера и self health-check).
func Addr() string {
	return config.String("MEDIA_ADDR", defaultAddr)
}

// dsn собирает строку подключения к базе сервиса (bozor_media) для host:port.
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
