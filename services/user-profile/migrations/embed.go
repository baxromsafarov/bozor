// Package migrations встраивает SQL-миграции User/Profile-сервиса в бинарь для
// применения при старте (см. pkg/shared/migrate).
package migrations

import "embed"

// FS — встроенные forward-only миграции goose User/Profile-сервиса.
//
//go:embed *.sql
var FS embed.FS
