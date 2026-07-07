package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Run("обязателен JWT_SIGNING_KEY", func(t *testing.T) {
		t.Setenv("JWT_SIGNING_KEY", "")
		_, err := Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JWT_SIGNING_KEY")
	})

	t.Run("значения по умолчанию", func(t *testing.T) {
		t.Setenv("JWT_SIGNING_KEY", "secret")
		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, ":8080", cfg.Addr)
		assert.Equal(t, "redis:6379", cfg.RedisAddr)
		assert.Equal(t, float64(20), cfg.RateRPS)
		assert.Equal(t, 40, cfg.RateBurst)
		assert.Equal(t, []string{"*"}, cfg.AllowedOrigins)
		assert.Equal(t, []byte("secret"), cfg.JWTSigningKey)
	})

	t.Run("переопределение из окружения", func(t *testing.T) {
		t.Setenv("JWT_SIGNING_KEY", "k")
		t.Setenv("GATEWAY_ADDR", ":9000")
		t.Setenv("RATE_LIMIT_RPS", "5")
		t.Setenv("RATE_LIMIT_BURST", "10")
		t.Setenv("CORS_ALLOWED_ORIGINS", "https://a.uz, https://b.uz")
		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, ":9000", cfg.Addr)
		assert.Equal(t, float64(5), cfg.RateRPS)
		assert.Equal(t, 10, cfg.RateBurst)
		assert.Equal(t, []string{"https://a.uz", "https://b.uz"}, cfg.AllowedOrigins)
	})
}

func TestUpstream(t *testing.T) {
	t.Run("значение по умолчанию из имени сервиса", func(t *testing.T) {
		assert.Equal(t, "http://listing-ads:8080", Upstream("listing-ads"))
	})

	t.Run("переопределение через UPSTREAM_<SERVICE>", func(t *testing.T) {
		t.Setenv("UPSTREAM_LISTING_ADS", "http://127.0.0.1:1234")
		assert.Equal(t, "http://127.0.0.1:1234", Upstream("listing-ads"))
	})
}
