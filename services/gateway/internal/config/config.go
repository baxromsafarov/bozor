// Package config собирает конфигурацию API Gateway из переменных окружения
// (12-factor) и валидирует обязательные ключи при старте.
package config

import (
	"fmt"
	"strings"

	"bozor/pkg/shared/config"
)

// Значения конфигурации по умолчанию.
const (
	defaultAddr        = ":8080"
	defaultRedisAddr   = "redis:6379"
	defaultRateRPS     = 20
	defaultRateBurst   = 40
	defaultCORSOrigins = "*"
)

// Config — конфигурация API Gateway.
type Config struct {
	Addr           string   // адрес прослушивания HTTP (GATEWAY_ADDR)
	Env            string   // окружение (APP_ENV)
	LogLevel       string   // уровень логирования (LOG_LEVEL)
	JWTSigningKey  []byte   // ключ проверки подписи access-JWT (JWT_SIGNING_KEY)
	RedisAddr      string   // адрес Redis для rate-limit (REDIS_ADDR)
	RedisPassword  string   // пароль Redis (REDIS_PASSWORD)
	RateRPS        float64  // скорость пополнения токенов, запросов/сек (RATE_LIMIT_RPS)
	RateBurst      int      // ёмкость bucket'а, всплеск (RATE_LIMIT_BURST)
	AllowedOrigins []string // разрешённые CORS-источники (CORS_ALLOWED_ORIGINS)
}

// Load читает конфигурацию из окружения. Возвращает ошибку, если не задан
// обязательный ключ JWT_SIGNING_KEY (fail-fast).
func Load() (*Config, error) {
	if missing := config.Missing("JWT_SIGNING_KEY"); len(missing) > 0 {
		return nil, fmt.Errorf("config: не заданы обязательные переменные: %s", strings.Join(missing, ", "))
	}
	return &Config{
		Addr:           Addr(),
		Env:            config.String("APP_ENV", "dev"),
		LogLevel:       config.String("LOG_LEVEL", "info"),
		JWTSigningKey:  []byte(config.String("JWT_SIGNING_KEY", "")),
		RedisAddr:      config.String("REDIS_ADDR", defaultRedisAddr),
		RedisPassword:  config.String("REDIS_PASSWORD", ""),
		RateRPS:        float64(config.Int("RATE_LIMIT_RPS", defaultRateRPS)),
		RateBurst:      config.Int("RATE_LIMIT_BURST", defaultRateBurst),
		AllowedOrigins: splitAndTrim(config.String("CORS_ALLOWED_ORIGINS", defaultCORSOrigins)),
	}, nil
}

// Addr возвращает адрес прослушивания gateway (для сервера и self health-check).
func Addr() string {
	return config.String("GATEWAY_ADDR", defaultAddr)
}

// Upstream возвращает базовый URL внутреннего сервиса: значение
// UPSTREAM_<SERVICE> (имя в верхнем регистре, дефисы → подчёркивания)
// либо http://<service>:8080 по умолчанию (DNS сервиса в сети compose).
func Upstream(service string) string {
	env := "UPSTREAM_" + strings.ToUpper(strings.ReplaceAll(service, "-", "_"))
	return config.String(env, "http://"+service+":8080")
}

// splitAndTrim разбивает строку по запятым и отбрасывает пустые элементы.
func splitAndTrim(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}
