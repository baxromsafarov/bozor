// Package migrations встраивает SQL-миграции Favorites/SavedSearch-сервиса в
// бинарь для применения при старте (см. pkg/shared/migrate).
package migrations

import "embed"

// FS — встроенные forward-only миграции goose Favorites/SavedSearch-сервиса.
//
//go:embed *.sql
var FS embed.FS
