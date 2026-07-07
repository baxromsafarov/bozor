// Package migrations встраивает SQL-миграции Catalog-сервиса в бинарь для
// применения при старте (см. pkg/shared/migrate).
package migrations

import "embed"

// FS — встроенные forward-only миграции goose Catalog-сервиса.
//
//go:embed *.sql
var FS embed.FS
