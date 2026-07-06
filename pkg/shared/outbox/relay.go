package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/pkg/shared/events"
)

// Компиляционные проверки: *pgxpool.Pool удовлетворяет Execer и Querier.
var (
	_ Execer  = (*pgxpool.Pool)(nil)
	_ Querier = (*pgxpool.Pool)(nil)
)

const (
	// defaultBatchSize — размер партии по умолчанию.
	defaultBatchSize = 100
	// defaultInterval — пауза между опросами outbox по умолчанию.
	defaultInterval = 500 * time.Millisecond
	// maxBackoff — верхняя граница экспоненциального backoff при ошибках.
	maxBackoff = 5 * time.Second
)

// PublishFunc публикует конверт события во внешнюю шину
// (например, NATS JetStream).
type PublishFunc func(ctx context.Context, env events.Envelope) error

// Relay — фоновый воркер, перекладывающий события из таблицы outbox
// во внешнюю шину. Безопасен для запуска в нескольких экземплярах:
// выборка идёт с FOR UPDATE SKIP LOCKED.
type Relay struct {
	// Pool — пул соединений с PostgreSQL.
	Pool *pgxpool.Pool
	// Publish — функция публикации события в шину.
	Publish PublishFunc
	// BatchSize — максимум событий за один проход (по умолчанию 100).
	BatchSize int
	// Interval — пауза между проходами при пустом outbox (по умолчанию 500ms).
	Interval time.Duration
	// Log — логгер; если nil, используется slog.Default().
	Log *slog.Logger
}

// message — строка таблицы outbox, подлежащая публикации.
type message struct {
	ID      string
	Payload []byte
}

// Run крутит цикл публикации до отмены ctx: вызывает RunOnce, при пустой
// партии ждёт Interval, при ошибке логирует её и делает экспоненциальный
// backoff (до 5s). Возвращает причину остановки — ошибку контекста.
func (r *Relay) Run(ctx context.Context) error {
	backoff := r.interval()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, err := r.RunOnce(ctx)

		var wait time.Duration
		switch {
		case err != nil:
			r.logger().ErrorContext(ctx, "outbox: ошибка прохода relay", "error", err)
			wait = backoff
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		case n == 0:
			backoff = r.interval()
			wait = r.interval()
		default:
			// Партия опубликована — сразу идём за следующей.
			backoff = r.interval()
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// RunOnce выполняет один проход: в транзакции выбирает неопубликованные
// события (FOR UPDATE SKIP LOCKED), публикует каждое через Publish и помечает
// успешно опубликованные published_at = now(). Ошибка публикации отдельного
// события логируется и не прерывает остальные — событие останется
// неопубликованным и будет повторено на следующем проходе.
// Возвращает число опубликованных событий.
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("outbox: начало транзакции: %w", err)
	}
	// Откат безопасен и после успешного Commit (вернёт pgx.ErrTxClosed).
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, payload FROM outbox WHERE published_at IS NULL ORDER BY created_at LIMIT $1 FOR UPDATE SKIP LOCKED`,
		r.batchSize(),
	)
	if err != nil {
		return 0, fmt.Errorf("outbox: выборка неопубликованных событий: %w", err)
	}

	var msgs []message
	for rows.Next() {
		var m message
		if err := rows.Scan(&m.ID, &m.Payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("outbox: чтение строки outbox: %w", err)
		}
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("outbox: итерация по строкам outbox: %w", err)
	}

	if len(msgs) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("outbox: фиксация пустой транзакции: %w", err)
		}
		return 0, nil
	}

	published := publishBatch(ctx, r.Publish, r.logger(), msgs)
	if len(published) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE outbox SET published_at = now() WHERE id = ANY($1)`,
			published,
		); err != nil {
			return 0, fmt.Errorf("outbox: пометка опубликованных событий: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("outbox: фиксация транзакции: %w", err)
	}
	return len(published), nil
}

// publishBatch публикует сообщения по одному. Ошибка десериализации или
// публикации отдельного сообщения логируется и не прерывает остальные.
// Возвращает id успешно опубликованных сообщений.
func publishBatch(ctx context.Context, publish PublishFunc, log *slog.Logger, msgs []message) []string {
	published := make([]string, 0, len(msgs))
	for _, m := range msgs {
		var env events.Envelope
		if err := json.Unmarshal(m.Payload, &env); err != nil {
			log.ErrorContext(ctx, "outbox: некорректный payload события",
				"event_id", m.ID, "error", err)
			continue
		}
		if err := publish(ctx, env); err != nil {
			log.ErrorContext(ctx, "outbox: ошибка публикации события",
				"event_id", m.ID, "subject", env.Type, "error", err)
			continue
		}
		published = append(published, m.ID)
	}
	return published
}

// logger возвращает настроенный логгер либо slog.Default().
func (r *Relay) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// batchSize возвращает размер партии с учётом значения по умолчанию.
func (r *Relay) batchSize() int {
	if r.BatchSize > 0 {
		return r.BatchSize
	}
	return defaultBatchSize
}

// interval возвращает интервал опроса с учётом значения по умолчанию.
func (r *Relay) interval() time.Duration {
	if r.Interval > 0 {
		return r.Interval
	}
	return defaultInterval
}
