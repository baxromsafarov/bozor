// Package migrations встраивает SQL-миграции Notification-сервиса в бинарь для
// применения при старте (см. pkg/shared/migrate).
package migrations

import "embed"

// FS — встроенные forward-only миграции goose Notification-сервиса.
//
//go:embed *.sql
var FS embed.FS
