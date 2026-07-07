package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("POSTGRES_USER", "bozor")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "hook-secret")
}

func TestLoad_RequiresKeys(t *testing.T) {
	t.Setenv("POSTGRES_USER", "")
	t.Setenv("POSTGRES_PASSWORD", "")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POSTGRES_USER")
	assert.Contains(t, err.Error(), "TELEGRAM_WEBHOOK_SECRET")
}

func TestLoad_DSNs(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	require.NoError(t, err)

	// Рантайм — через PgBouncer; миграции — напрямую к Postgres (ADR-013).
	assert.Contains(t, cfg.AppDSN, "@pgbouncer:6432/bozor_auth")
	assert.Contains(t, cfg.MigrateDSN, "@postgres:5432/bozor_auth")
	assert.Contains(t, cfg.AppDSN, "sslmode=disable")
	assert.Equal(t, "hook-secret", cfg.TelegramWebhookSecret)
	assert.Equal(t, ":8080", cfg.Addr)
}

func TestLoad_HostOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("PGBOUNCER_HOST", "pgb")
	t.Setenv("POSTGRES_HOST", "pg")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Contains(t, cfg.AppDSN, "@pgb:6432/")
	assert.Contains(t, cfg.MigrateDSN, "@pg:5432/")
}
