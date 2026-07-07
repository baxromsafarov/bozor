// Package migrate — единый forward-only раннер миграций goose для сервисов
// Bozor. Каждый сервис встраивает свои SQL-миграции через embed.FS и вызывает
// Up при старте; конкурентные реплики сериализуются advisory-локом Postgres,
// поэтому миграции применяет ровно один инстанс (ADR-007).
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib" // регистрирует sql-драйвер "pgx"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Up применяет все ожидающие forward-миграции из fsys к базе по dsn и
// возвращает число применённых миграций. Down-секции не выполняются никогда
// (forward-only): исправление — только новой миграцией вперёд. Повторный вызов
// без новых миграций — no-op (возврат 0). Для сериализации нескольких реплик
// берётся сессионный advisory-lock Postgres.
//
// fsys должен быть корнем каталога миграций (файлы вида `00001_name.sql`
// с секциями `-- +goose Up` / `-- +goose Down`). Обычно передаётся
// fs.Sub(embedded, "migrations").
func Up(ctx context.Context, dsn string, fsys fs.FS) (int, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return 0, fmt.Errorf("migrate: открытие БД: %w", err)
	}
	defer func() { _ = db.Close() }()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return 0, fmt.Errorf("migrate: создание advisory-locker: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithSessionLocker(locker))
	if err != nil {
		return 0, fmt.Errorf("migrate: провайдер goose: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return 0, fmt.Errorf("migrate: применение миграций: %w", err)
	}
	return len(results), nil
}
