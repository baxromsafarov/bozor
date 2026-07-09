package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_RequiresAPIKey(t *testing.T) {
	t.Setenv("TYPESENSE_API_KEY", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TYPESENSE_API_KEY")
}

func TestLoad_BuildsTypesenseURL(t *testing.T) {
	t.Setenv("TYPESENSE_API_KEY", "secret")
	t.Setenv("TYPESENSE_HOST", "ts")
	t.Setenv("TYPESENSE_PORT", "9999")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "http://ts:9999", cfg.TypesenseURL)
	assert.Equal(t, "secret", cfg.TypesenseAPIKey)
	assert.Equal(t, ":8080", cfg.Addr)
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("TYPESENSE_API_KEY", "secret")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "http://typesense:8108", cfg.TypesenseURL)
	assert.Equal(t, "dev", cfg.Env)
}
