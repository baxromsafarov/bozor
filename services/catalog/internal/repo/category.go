// Package repo содержит репозитории Catalog-сервиса (PostgreSQL через pgx).
package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/catalog/internal/domain"
)

const selectColumns = `id, parent_id, slug, name_uz, name_ru, level, path, sort_order, is_active`

// Repo — репозиторий категорий.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// All возвращает все категории, упорядоченные для сборки дерева.
func (r *Repo) All(ctx context.Context) ([]domain.Category, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+selectColumns+` FROM categories ORDER BY level, sort_order, id`)
	if err != nil {
		return nil, fmt.Errorf("repo: запрос категорий: %w", err)
	}
	defer rows.Close()

	var out []domain.Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: итерация категорий: %w", err)
	}
	return out, nil
}

// GetByID возвращает категорию по id (domain.ErrCategoryNotFound если нет).
func (r *Repo) GetByID(ctx context.Context, id string) (domain.Category, error) {
	c, err := scanCategory(r.pool.QueryRow(ctx, `SELECT `+selectColumns+` FROM categories WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Category{}, domain.ErrCategoryNotFound
	}
	if err != nil {
		return domain.Category{}, fmt.Errorf("repo: запрос категории: %w", err)
	}
	return c, nil
}

// CreateWithEvent вставляет категорию и кладёт событие в outbox одной транзакцией.
func (r *Repo) CreateWithEvent(ctx context.Context, c domain.Category, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO categories (id, parent_id, slug, name_uz, name_ru, level, path, sort_order, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, c.ID, c.ParentID, c.Slug, c.NameUZ, c.NameRU, c.Level, c.Path, c.SortOrder, c.IsActive)
		if isUniqueViolation(err) {
			return domain.ErrSlugConflict
		}
		if err != nil {
			return fmt.Errorf("repo: вставка категории: %w", err)
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// UpdateWithEvent обновляет изменяемые поля (имена, порядок, активность) и
// публикует событие. slug/parent неизменяемы (стабильность path).
func (r *Repo) UpdateWithEvent(ctx context.Context, c domain.Category, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE categories SET name_uz = $2, name_ru = $3, sort_order = $4, is_active = $5, updated_at = now()
			WHERE id = $1
		`, c.ID, c.NameUZ, c.NameRU, c.SortOrder, c.IsActive)
		if err != nil {
			return fmt.Errorf("repo: обновление категории: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrCategoryNotFound
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// DeleteWithEvent удаляет категорию (запрещено при наличии потомков) и
// публикует событие.
func (r *Repo) DeleteWithEvent(ctx context.Context, id string, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var children int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM categories WHERE parent_id = $1`, id).Scan(&children); err != nil {
			return fmt.Errorf("repo: проверка потомков: %w", err)
		}
		if children > 0 {
			return domain.ErrHasChildren
		}
		tag, err := tx.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("repo: удаление категории: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrCategoryNotFound
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// scanCategory читает строку категории.
func scanCategory(row pgx.Row) (domain.Category, error) {
	var c domain.Category
	err := row.Scan(&c.ID, &c.ParentID, &c.Slug, &c.NameUZ, &c.NameRU,
		&c.Level, &c.Path, &c.SortOrder, &c.IsActive)
	return c, err
}

// isUniqueViolation сообщает, что ошибка — нарушение UNIQUE (slug).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
