// Package repo содержит репозитории Auth-сервиса (PostgreSQL через pgx).
package repo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/auth/internal/domain"
)

// UserRepo — репозиторий пользователей.
type UserRepo struct {
	pool *pgxpool.Pool
}

// NewUserRepo создаёт репозиторий поверх пула соединений.
func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

// UpsertUserWithEvent атомарно апсертит пользователя по telegram_user_id и,
// если он создан впервые (created=true), кладёт событие ev в outbox в той же
// транзакции. Так соблюдается transactional outbox: событие публикуется
// ровно тогда, когда пользователь действительно появился.
func (r *UserRepo) UpsertUserWithEvent(ctx context.Context, u domain.User, ev events.Envelope) (bool, error) {
	var created bool
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		c, e := upsertUser(ctx, tx, u)
		if e != nil {
			return e
		}
		created = c
		if created {
			return outbox.Enqueue(ctx, tx, ev)
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("repo: upsert пользователя: %w", err)
	}
	return created, nil
}

// upsertUser вставляет или обновляет пользователя. Признак «создан впервые»
// определяется через (xmax = 0): для INSERT xmax = 0, для UPDATE — нет.
func upsertUser(ctx context.Context, tx pgx.Tx, u domain.User) (bool, error) {
	var created bool
	err := tx.QueryRow(ctx, `
		INSERT INTO users (id, telegram_user_id, phone, username, first_name, last_name, language_code)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (telegram_user_id) DO UPDATE SET
			phone         = EXCLUDED.phone,
			username      = EXCLUDED.username,
			first_name    = EXCLUDED.first_name,
			last_name     = EXCLUDED.last_name,
			language_code = EXCLUDED.language_code,
			updated_at    = now()
		RETURNING (xmax = 0) AS created
	`, u.ID, u.TelegramUserID, u.Phone, u.Username, u.FirstName, u.LastName, u.LanguageCode).Scan(&created)
	if err != nil {
		return false, fmt.Errorf("repo: запрос upsert users: %w", err)
	}
	return created, nil
}
