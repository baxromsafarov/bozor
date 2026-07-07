// Package repo содержит репозитории Media-сервиса (PostgreSQL через pgx).
package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/media/internal/domain"
)

const selectColumns = `id, owner_user_id, ad_id, bucket, object_key, mime_type,
	size_bytes, status, width, height, created_at`

// Repo — репозиторий медиа.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// CountByAd возвращает число медиа, привязанных к объявлению.
func (r *Repo) CountByAd(ctx context.Context, adID string) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM media WHERE ad_id = $1`, adID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: подсчёт медиа объявления: %w", err)
	}
	return n, nil
}

// InsertWithEvent вставляет запись о медиа и кладёт событие в outbox одной транзакцией.
func (r *Repo) InsertWithEvent(ctx context.Context, m domain.Media, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO media (id, owner_user_id, ad_id, bucket, object_key, mime_type,
				size_bytes, status, width, height, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, m.ID, m.OwnerUserID, m.AdID, m.Bucket, m.ObjectKey, m.MimeType,
			m.SizeBytes, string(m.Status), m.Width, m.Height, m.CreatedAt)
		if err != nil {
			return fmt.Errorf("repo: вставка медиа: %w", err)
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// GetByID возвращает медиа по id (domain.ErrMediaNotFound если нет).
func (r *Repo) GetByID(ctx context.Context, id string) (domain.Media, error) {
	m, err := scanMedia(r.pool.QueryRow(ctx, `SELECT `+selectColumns+` FROM media WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Media{}, domain.ErrMediaNotFound
	}
	if err != nil {
		return domain.Media{}, fmt.Errorf("repo: запрос медиа: %w", err)
	}
	return m, nil
}

// scanMedia читает строку медиа.
func scanMedia(row pgx.Row) (domain.Media, error) {
	var (
		m      domain.Media
		status string
	)
	err := row.Scan(&m.ID, &m.OwnerUserID, &m.AdID, &m.Bucket, &m.ObjectKey, &m.MimeType,
		&m.SizeBytes, &status, &m.Width, &m.Height, &m.CreatedAt)
	m.Status = domain.Status(status)
	return m, err
}
