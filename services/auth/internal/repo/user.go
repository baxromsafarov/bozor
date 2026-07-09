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
// ровно тогда, когда пользователь действительно появился. Возвращает
// фактический id пользователя в БД (существующий при конфликте, новый при
// вставке) — он нужен для привязки логин-сессии.
func (r *UserRepo) UpsertUserWithEvent(ctx context.Context, u domain.User, ev events.Envelope) (string, bool, error) {
	var (
		id      string
		created bool
	)
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		i, c, e := upsertUser(ctx, tx, u)
		if e != nil {
			return e
		}
		id, created = i, c
		if created {
			return outbox.Enqueue(ctx, tx, ev)
		}
		return nil
	})
	if err != nil {
		return "", false, fmt.Errorf("repo: upsert пользователя: %w", err)
	}
	return id, created, nil
}

// BanUser помечает пользователя забаненным и отзывает все его refresh-токены
// одной транзакцией (реакция на bozor.user.banned). Идемпотентно: повторная
// доставка события не меняет результат. Возвращает число отозванных токенов.
func (r *UserRepo) BanUser(ctx context.Context, userID string) (int64, error) {
	var revoked int64
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx,
			`UPDATE users SET status = 'banned', updated_at = now() WHERE id = $1 AND status <> 'deleted'`,
			userID); e != nil {
			return fmt.Errorf("repo: пометка бана пользователя: %w", e)
		}
		tag, e := tx.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
			userID)
		if e != nil {
			return fmt.Errorf("repo: отзыв refresh-токенов: %w", e)
		}
		revoked = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return revoked, nil
}

// upsertUser вставляет или обновляет пользователя, возвращая его id и признак
// «создан впервые». Последний определяется через (xmax = 0): для INSERT
// xmax = 0, для UPDATE — нет. RETURNING id даёт фактический id строки:
// при конфликте это существующий id, а не сгенерированный кандидат u.ID.
func upsertUser(ctx context.Context, tx pgx.Tx, u domain.User) (string, bool, error) {
	var (
		id      string
		created bool
	)
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
		RETURNING id, (xmax = 0) AS created
	`, u.ID, u.TelegramUserID, u.Phone, u.Username, u.FirstName, u.LastName, u.LanguageCode).Scan(&id, &created)
	if err != nil {
		return "", false, fmt.Errorf("repo: запрос upsert users: %w", err)
	}
	return id, created, nil
}
