// Package migrations встраивает SQL-миграции Chat-сервиса в бинарь для
// применения при старте (см. pkg/shared/migrate).
package migrations

import "embed"

// FS — встроенные forward-only миграции goose Chat-сервиса.
//
//go:embed *.sql
var FS embed.FS
