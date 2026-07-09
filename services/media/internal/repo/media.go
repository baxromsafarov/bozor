// Package repo содержит репозитории Media-сервиса (PostgreSQL через pgx).
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/media/internal/domain"
)

const selectColumns = `id, owner_user_id, ad_id, bucket, object_key, mime_type,
	size_bytes, status, width, height, previews, processed_at, created_at`

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

// IsEventProcessed сообщает, обрабатывал ли consumer событие eventID (inbox).
func (r *Repo) IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error) {
	return outbox.AlreadyProcessed(ctx, r.pool, consumer, eventID)
}

// MarkEventProcessed помечает событие обработанным без иных изменений
// (для пропуска: медиа уже готово/удалено, повторная обработка не нужна).
func (r *Repo) MarkEventProcessed(ctx context.Context, consumer, eventID string) error {
	return outbox.MarkProcessed(ctx, r.pool, consumer, eventID)
}

// MarkProcessedWithEvent атомарно переводит медиа в статус ready (размеры,
// превью, момент обработки), публикует bozor.media.processed через outbox и
// отмечает событие обработанным (inbox) — всё в одной транзакции.
// Переход выполняется только из статуса uploaded (идемпотентность): повторная
// доставка не плодит событие, но всё равно отмечает событие обработанным.
func (r *Repo) MarkProcessedWithEvent(ctx context.Context, consumer, eventID string, m domain.Media, ev events.Envelope) error {
	previews, err := json.Marshal(m.Previews)
	if err != nil {
		return fmt.Errorf("repo: сериализация превью: %w", err)
	}
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE media SET width = $2, height = $3, status = $4,
				processed_at = now(), previews = $5
			WHERE id = $1 AND status = $6
		`, m.ID, m.Width, m.Height, string(domain.StatusReady), previews, string(domain.StatusUploaded))
		if err != nil {
			return fmt.Errorf("repo: обновление медиа: %w", err)
		}
		if tag.RowsAffected() > 0 {
			if err := outbox.Enqueue(ctx, tx, ev); err != nil {
				return err
			}
		}
		return outbox.MarkProcessed(ctx, tx, consumer, eventID)
	})
}

// ListOrphans возвращает непривязанные к объявлению медиа (ad_id IS NULL),
// созданные раньше olderThan, — кандидаты на очистку (не более limit за проход).
func (r *Repo) ListOrphans(ctx context.Context, olderThan time.Time, limit int) ([]domain.Media, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+selectColumns+`
		FROM media WHERE ad_id IS NULL AND created_at < $1
		ORDER BY created_at LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: выборка сирот: %w", err)
	}
	defer rows.Close()

	var out []domain.Media
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: чтение сироты: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: перебор сирот: %w", err)
	}
	return out, nil
}

// DeleteWithEvent удаляет запись о медиа и публикует событие в outbox одной
// транзакцией (domain.ErrMediaNotFound, если записи уже нет).
func (r *Repo) DeleteWithEvent(ctx context.Context, id string, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM media WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("repo: удаление медиа: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrMediaNotFound
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// scanMedia читает строку медиа.
func scanMedia(row pgx.Row) (domain.Media, error) {
	var (
		m        domain.Media
		status   string
		previews []byte
	)
	err := row.Scan(&m.ID, &m.OwnerUserID, &m.AdID, &m.Bucket, &m.ObjectKey, &m.MimeType,
		&m.SizeBytes, &status, &m.Width, &m.Height, &previews, &m.ProcessedAt, &m.CreatedAt)
	if err != nil {
		return domain.Media{}, err
	}
	m.Status = domain.Status(status)
	if len(previews) > 0 {
		if err := json.Unmarshal(previews, &m.Previews); err != nil {
			return domain.Media{}, fmt.Errorf("repo: разбор превью: %w", err)
		}
	}
	return m, nil
}
