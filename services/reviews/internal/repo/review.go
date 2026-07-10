// Package repo содержит репозиторий Reviews-сервиса (PostgreSQL через pgx).
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

	"bozor/services/reviews/internal/domain"
)

// Repo — репозиторий отзывов.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

const reviewColumns = `id, ad_id, author_id, target_id, rating, body, status, created_at, updated_at`

// CreateWithEvent вставляет отзыв и кладёт bozor.review.created в outbox — одной
// транзакцией. Нарушение уникальности (ad_id, author_id) → ErrDuplicateReview
// (один отзыв на взаимодействие), событие при этом не публикуется.
func (r *Repo) CreateWithEvent(ctx context.Context, rev domain.Review, ev events.Envelope) error {
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO reviews (id, ad_id, author_id, target_id, rating, body, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
			rev.ID, rev.AdID, rev.AuthorID, rev.TargetID, rev.Rating, rev.Body, rev.Status, rev.CreatedAt)
		if err != nil {
			return fmt.Errorf("repo: вставка отзыва: %w", err)
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
	if isUniqueViolation(err) {
		return domain.ErrDuplicateReview
	}
	return err
}

// ListByTarget возвращает активные отзывы о пользователе (свежие сверху),
// постранично. Снятые модератором (blocked) в публичную ленту не попадают.
func (r *Repo) ListByTarget(ctx context.Context, targetID string, limit, offset int) ([]domain.Review, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+reviewColumns+`
		FROM reviews WHERE target_id = $1 AND status = 'active'
		ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, targetID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("repo: запрос отзывов: %w", err)
	}
	defer rows.Close()

	var out []domain.Review
	for rows.Next() {
		rev, err := scanReview(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: чтение отзыва: %w", err)
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

// AggregateRating возвращает агрегат рейтинга продавца по активным отзывам
// (средняя оценка и количество). Снятые (blocked) отзывы не учитываются. Нет
// отзывов → нулевой рейтинг (0/0). Источник истины для кеша Profile (9.2).
func (r *Repo) AggregateRating(ctx context.Context, targetID string) (domain.Rating, error) {
	var rt domain.Rating
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(AVG(rating), 0)
		FROM reviews WHERE target_id = $1 AND status = 'active'`, targetID).
		Scan(&rt.ReviewsCount, &rt.AvgRating)
	if err != nil {
		return domain.Rating{}, fmt.Errorf("repo: агрегат рейтинга %s: %w", targetID, err)
	}
	return rt, nil
}

// BlockReview скрывает отзыв (status → blocked) по решению модерации. Идемпотентно:
// повторное снятие не затрагивает уже снятый отзыв.
func (r *Repo) BlockReview(ctx context.Context, reviewID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE reviews SET status = 'blocked', updated_at = now()
		WHERE id = $1 AND status = 'active'`, reviewID)
	if err != nil {
		return fmt.Errorf("repo: снятие отзыва %s: %w", reviewID, err)
	}
	return nil
}

// IsEventProcessed сообщает, обрабатывал ли consumer событие eventID (inbox).
func (r *Repo) IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error) {
	return outbox.AlreadyProcessed(ctx, r.pool, consumer, eventID)
}

// MarkEventProcessed помечает событие обработанным (inbox).
func (r *Repo) MarkEventProcessed(ctx context.Context, consumer, eventID string) error {
	return outbox.MarkProcessed(ctx, r.pool, consumer, eventID)
}

// scanReview читает строку отзыва.
func scanReview(row pgx.Row) (domain.Review, error) {
	var rev domain.Review
	err := row.Scan(&rev.ID, &rev.AdID, &rev.AuthorID, &rev.TargetID, &rev.Rating,
		&rev.Body, &rev.Status, &rev.CreatedAt, &rev.UpdatedAt)
	return rev, err
}

// isUniqueViolation сообщает, является ли ошибка нарушением уникального индекса (23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
