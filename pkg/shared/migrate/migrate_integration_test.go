//go:build integration

package migrate

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

//go:embed testdata/migrations/*.sql
var testMigrations embed.FS

// TestUp_Integration поднимает реальный PostgreSQL в контейнере и проверяет:
// применение с нуля, применение сида и идемпотентность повторного прогона.
// Запуск: go test -tags=integration ./migrate/...
func TestUp_Integration(t *testing.T) {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_test"),
		tcpostgres.WithUsername("bozor"),
		tcpostgres.WithPassword("bozor"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	sub, err := fs.Sub(testMigrations, "testdata/migrations")
	require.NoError(t, err)

	// Прогон с нуля: 2 миграции (схема + сид).
	n, err := Up(ctx, dsn, sub)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "с нуля применяются обе миграции")

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Сид применился.
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM demo_items").Scan(&count))
	assert.Equal(t, 2, count, "сид вставил справочные строки")

	// Повторный прогон на существующей БД: миграций нет — no-op.
	n, err = Up(ctx, dsn, sub)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "повторный прогон идемпотентен")

	// Данные сида не задвоились.
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM demo_items").Scan(&count))
	assert.Equal(t, 2, count, "повторное применение не дублирует сид")
}
