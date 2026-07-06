// Package pgxx содержит вспомогательные функции для работы с PostgreSQL
// через pgx/pgxpool: создание пула соединений и выполнение транзакций.
package pgxx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultMaxConns — размер пула по умолчанию, если MaxConns не задан в DSN.
const defaultMaxConns = 8

// pingTimeout — таймаут проверки соединения при создании пула.
const pingTimeout = 5 * time.Second

// NewPool создаёт пул соединений к PostgreSQL по DSN и проверяет
// доступность БД (Ping с таймаутом 5 секунд). Если параметр pool_max_conns
// не задан в DSN, используется значение по умолчанию (8). При неудачном
// Ping пул закрывается и возвращается ошибка.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxx: разбор DSN: %w", err)
	}
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = defaultMaxConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxx: создание пула: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgxx: проверка соединения: %w", err)
	}
	return pool, nil
}

// WithTx выполняет fn внутри транзакции: Begin → fn → Commit.
// При ошибке fn или Commit выполняется Rollback; ошибка Rollback
// не теряется — объединяется с исходной через errors.Join.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgxx: начало транзакции: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(fmt.Errorf("pgxx: транзакция: %w", err), fmt.Errorf("pgxx: откат: %w", rbErr))
		}
		return fmt.Errorf("pgxx: транзакция: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(fmt.Errorf("pgxx: фиксация транзакции: %w", err), fmt.Errorf("pgxx: откат: %w", rbErr))
		}
		return fmt.Errorf("pgxx: фиксация транзакции: %w", err)
	}
	return nil
}
