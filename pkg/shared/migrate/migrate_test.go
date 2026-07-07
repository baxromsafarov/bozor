package migrate

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUp_BadDSN(t *testing.T) {
	_, err := Up(context.Background(), "postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable&connect_timeout=1",
		fstest.MapFS{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migrate:")
}

func TestUp_UnparsableDSN(t *testing.T) {
	_, err := Up(context.Background(), "://not-a-dsn", fstest.MapFS{})
	require.Error(t, err)
}
