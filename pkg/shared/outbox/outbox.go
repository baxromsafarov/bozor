// Package outbox реализует паттерн transactional outbox для надёжной
// публикации доменных событий и inbox-идемпотентность для их обработки.
//
// Схема работы: сервис в одной транзакции с бизнес-данными кладёт событие
// в таблицу outbox через Enqueue, а фоновый Relay вычитывает неопубликованные
// события и отправляет их в шину (например, NATS JetStream). На стороне
// потребителя AlreadyProcessed/MarkProcessed обеспечивают обработку
// «ровно один раз» на уровне консьюмера.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"bozor/pkg/shared/events"
)

// MigrationSQL — DDL таблицы outbox с частичным индексом по неопубликованным
// событиям. Выполняется при миграции схемы сервиса-издателя.
const MigrationSQL = `
CREATE TABLE IF NOT EXISTS outbox (
    id           uuid PRIMARY KEY,
    subject      text NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox (created_at)
    WHERE published_at IS NULL;
`

// Execer — минимальный интерфейс выполнения SQL-команд.
// Ему удовлетворяют pgx.Tx и *pgxpool.Pool, поэтому Enqueue можно вызывать
// как внутри бизнес-транзакции, так и на пуле напрямую.
type Execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// Querier — минимальный интерфейс однострочных SQL-запросов.
// Ему удовлетворяют pgx.Tx и *pgxpool.Pool.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Компиляционные проверки: интерфейс pgx.Tx удовлетворяет Execer и Querier
// (аналогичные проверки для *pgxpool.Pool — в relay.go).
var (
	_ Execer  = pgx.Tx(nil)
	_ Querier = pgx.Tx(nil)
)

// Enqueue кладёт конверт события в таблицу outbox. Вызывается внутри той же
// транзакции, что и изменение бизнес-данных: id берётся из env.ID, subject —
// из env.Type, payload — полный конверт в JSON.
func Enqueue(ctx context.Context, db Execer, env events.Envelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("outbox: сериализация конверта события: %w", err)
	}

	_, err = db.Exec(ctx,
		`INSERT INTO outbox (id, subject, payload) VALUES ($1, $2, $3)`,
		env.ID, env.Type, payload,
	)
	if err != nil {
		return fmt.Errorf("outbox: вставка события %s в outbox: %w", env.ID, err)
	}
	return nil
}
