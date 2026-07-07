// Package migrations встраивает SQL-миграции Auth-сервиса в бинарь для
// применения при старте (см. pkg/shared/migrate).
package migrations

import "embed"

// FS — встроенные forward-only миграции goose Auth-сервиса.
//
//go:embed *.sql
var FS embed.FS
