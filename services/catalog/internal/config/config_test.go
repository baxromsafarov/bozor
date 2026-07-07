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

func TestLoad_DSNsAndDefaults(t *testing.T) {
	t.Setenv("POSTGRES_USER", "bozor")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	cfg, err := Load()
	require.NoError(t, err)

	assert.Contains(t, cfg.AppDSN, "@pgbouncer:6432/bozor_catalog")
	assert.Contains(t, cfg.MigrateDSN, "@postgres:5432/bozor_catalog")
	assert.Equal(t, ":8080", cfg.Addr)
	assert.Equal(t, "nats://nats:4222", cfg.NATSURL)
	assert.Equal(t, "redis:6379", cfg.RedisAddr)
}
