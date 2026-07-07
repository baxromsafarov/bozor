package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_RequiresKeys(t *testing.T) {
	t.Setenv("POSTGRES_USER", "")
	t.Setenv("POSTGRES_PASSWORD", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POSTGRES_USER")
}

func TestLoad_DSNs(t *testing.T) {
	t.Setenv("POSTGRES_USER", "bozor")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	cfg, err := Load()
	require.NoError(t, err)

	// Рантайм — через PgBouncer; миграции — напрямую к Postgres (ADR-013).
	assert.Contains(t, cfg.AppDSN, "@pgbouncer:6432/bozor_location")
	assert.Contains(t, cfg.MigrateDSN, "@postgres:5432/bozor_location")
	assert.Contains(t, cfg.AppDSN, "sslmode=disable")
	assert.Equal(t, ":8080", cfg.Addr)
}
