// Package config собирает конфигурацию Search-сервиса из переменных окружения.
package config

import (
	"fmt"
	"strings"

	"bozor/pkg/shared/config"
)

const defaultAddr = ":8080"

// Config — конфигурация Search-сервиса.
type Config struct {
	Addr     string
	Env      string
	LogLevel string

	// TypesenseURL — базовый URL Typesense (http://host:port).
	TypesenseURL string
	// TypesenseAPIKey — ключ доступа к Typesense (обязателен).
	TypesenseAPIKey string

	// NATSURL — адрес NATS JetStream (потребление bozor.ad.* индексатором).
	NATSURL string
	// ListingInternalURL — базовый URL внутренних read-эндпоинтов Listing.
	ListingInternalURL string
}

// Load читает конфигурацию из окружения (fail-fast на обязательных ключах).
func Load() (*Config, error) {
	if missing := config.Missing("TYPESENSE_API_KEY"); len(missing) > 0 {
		return nil, fmt.Errorf("config: не заданы обязательные переменные: %s", strings.Join(missing, ", "))
	}
	return &Config{
		Addr:               config.String("SEARCH_ADDR", defaultAddr),
		Env:                config.String("APP_ENV", "dev"),
		LogLevel:           config.String("LOG_LEVEL", "info"),
		TypesenseURL:       typesenseURL(config.String("TYPESENSE_HOST", "typesense"), config.String("TYPESENSE_PORT", "8108")),
		TypesenseAPIKey:    config.String("TYPESENSE_API_KEY", ""),
		NATSURL:            config.String("NATS_URL", "nats://nats:4222"),
		ListingInternalURL: config.String("LISTING_INTERNAL_URL", "http://listing-ads:8080"),
	}, nil
}

// Addr возвращает адрес прослушивания сервиса (для сервера и self health-check).
func Addr() string {
	return config.String("SEARCH_ADDR", defaultAddr)
}

// typesenseURL собирает базовый URL Typesense из host:port.
func typesenseURL(host, port string) string {
	return "http://" + host + ":" + port
}
