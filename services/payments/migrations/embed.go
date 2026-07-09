// Package migrations встраивает SQL-миграции Payments/Promotions-сервиса в бинарь
// для применения при старте (см. pkg/shared/migrate).
package migrations

import "embed"

// FS — встроенные forward-only миграции goose Payments/Promotions-сервиса.
//
//go:embed *.sql
var FS embed.FS
