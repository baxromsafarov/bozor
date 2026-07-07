package migrate_test

import (
	"context"
	"embed"
	"io/fs"
	"log"

	"bozor/pkg/shared/migrate"
)

// Каждый сервис встраивает свой каталог миграций в бинарь.
//
//go:embed testdata/migrations/*.sql
var serviceMigrations embed.FS

// ExampleUp показывает, как сервис применяет свои миграции при старте.
//
// ВАЖНО: DSN должен указывать НАПРЯМУЮ на PostgreSQL (порт 5432), а не через
// PgBouncer (6432): advisory-lock у goose — сессионный, а transaction-пулинг
// PgBouncer не гарантирует стабильную сессию (см. ADR-013).
func ExampleUp() {
	sub, err := fs.Sub(serviceMigrations, "testdata/migrations")
	if err != nil {
		log.Fatal(err)
	}

	const dsn = "postgres://bozor:secret@postgres:5432/bozor_auth?sslmode=disable"
	applied, err := migrate.Up(context.Background(), dsn, sub)
	if err != nil {
		log.Fatalf("миграции: %v", err)
	}
	log.Printf("применено миграций: %d", applied)
}
