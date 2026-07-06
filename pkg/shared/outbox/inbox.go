package outbox

import (
	"context"
	"fmt"
)

// InboxMigrationSQL — DDL таблицы processed_events для inbox-идемпотентности
// на стороне потребителя событий.
const InboxMigrationSQL = `
CREATE TABLE IF NOT EXISTS processed_events (
    consumer     text NOT NULL,
    event_id     uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
`

// AlreadyProcessed сообщает, обрабатывал ли уже consumer событие eventID.
// Используется перед обработкой входящего события для защиты от повторов.
func AlreadyProcessed(ctx context.Context, db Querier, consumer, eventID string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM processed_events WHERE consumer = $1 AND event_id = $2)`,
		consumer, eventID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("outbox: проверка обработки события %s потребителем %s: %w", eventID, consumer, err)
	}
	return exists, nil
}

// MarkProcessed отмечает событие eventID как обработанное потребителем
// consumer. Повторная отметка безопасна (ON CONFLICT DO NOTHING); вызывается
// в одной транзакции с побочными эффектами обработчика.
func MarkProcessed(ctx context.Context, db Execer, consumer, eventID string) error {
	_, err := db.Exec(ctx,
		`INSERT INTO processed_events (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		consumer, eventID,
	)
	if err != nil {
		return fmt.Errorf("outbox: отметка события %s потребителем %s: %w", eventID, consumer, err)
	}
	return nil
}
