// Package migrations встраивает SQL-миграции Reviews-сервиса в бинарь для
// применения при старте (см. pkg/shared/migrate).
package migrations

import "embed"

// FS — встроенные forward-only миграции goose Reviews-сервиса.
//
//go:embed *.sql
var FS embed.FS
