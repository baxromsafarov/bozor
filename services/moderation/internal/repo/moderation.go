// Package repo содержит репозиторий Moderation-сервиса (PostgreSQL через pgx):
// задачи модерации, справочники стоп-слов и запрещённых категорий, inbox/outbox.
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/moderation/internal/domain"
)

// Repo — репозиторий модерации.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// ActiveStopwords возвращает активные стоп-слова (uz/ru) для авто-проверки.
func (r *Repo) ActiveStopwords(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT word FROM stopwords WHERE active ORDER BY word`)
	if err != nil {
		return nil, fmt.Errorf("repo: чтение стоп-слов: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, fmt.Errorf("repo: скан стоп-слова: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: обход стоп-слов: %w", err)
	}
	return out, nil
}

// IsForbiddenCategory сообщает, входит ли категория в список запрещённых.
func (r *Repo) IsForbiddenCategory(ctx context.Context, categoryID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM forbidden_categories WHERE category_id = $1)`, categoryID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repo: проверка запрещённой категории: %w", err)
	}
	return exists, nil
}

// HasDuplicate сообщает, есть ли у пользователя другое (не excludeAdID) объявление
// с тем же нормализованным содержимым — простая детекция дублей.
func (r *Repo) HasDuplicate(ctx context.Context, userID, contentHash, excludeAdID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM moderation_tasks
			WHERE user_id = $1 AND content_hash = $2 AND ad_id <> $3
		)`, userID, contentHash, excludeAdID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repo: детекция дублей: %w", err)
	}
	return exists, nil
}

// AlreadyProcessed сообщает, обрабатывал ли consumer событие eventID (inbox).
func (r *Repo) AlreadyProcessed(ctx context.Context, consumer, eventID string) (bool, error) {
	return outbox.AlreadyProcessed(ctx, r.pool, consumer, eventID)
}

// SaveApprovedTask атомарно фиксирует авто-одобренную задачу (upsert по ad_id),
// отмечает событие обработанным (inbox) и кладёт bozor.ad.approved в outbox.
func (r *Repo) SaveApprovedTask(ctx context.Context, consumer, eventID string, t domain.Task, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if err := upsertTask(ctx, tx, t); err != nil {
			return err
		}
		if err := outbox.MarkProcessed(ctx, tx, consumer, eventID); err != nil {
			return err
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// SaveManualTask атомарно фиксирует задачу в ручной очереди (не прошла авто-проверки)
// и отмечает событие обработанным (inbox). Событие не публикуется.
func (r *Repo) SaveManualTask(ctx context.Context, consumer, eventID string, t domain.Task) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if err := upsertTask(ctx, tx, t); err != nil {
			return err
		}
		return outbox.MarkProcessed(ctx, tx, consumer, eventID)
	})
}

// MarkProcessed отмечает событие обработанным без записи задачи (объявление
// удалено/не в статусе pending).
func (r *Repo) MarkProcessed(ctx context.Context, consumer, eventID string) error {
	return outbox.MarkProcessed(ctx, r.pool, consumer, eventID)
}

// upsertTask вставляет/обновляет задачу по ad_id (повторная модерация правки
// перезаписывает актуальную задачу).
func upsertTask(ctx context.Context, tx pgx.Tx, t domain.Task) error {
	reasons, err := json.Marshal(t.Reasons)
	if err != nil {
		return fmt.Errorf("repo: сериализация причин: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO moderation_tasks (id, ad_id, user_id, title, content_hash, status, auto_result, reasons)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (ad_id) DO UPDATE
		SET user_id = EXCLUDED.user_id, title = EXCLUDED.title, content_hash = EXCLUDED.content_hash,
		    status = EXCLUDED.status, auto_result = EXCLUDED.auto_result, reasons = EXCLUDED.reasons,
		    updated_at = now()
	`, t.ID, t.AdID, t.UserID, t.Title, t.ContentHash, t.Status, t.AutoResult, reasons)
	if err != nil {
		return fmt.Errorf("repo: upsert задачи модерации: %w", err)
	}
	return nil
}

// GetTask возвращает задачу модерации по ad_id. found=false, если задачи нет.
func (r *Repo) GetTask(ctx context.Context, adID string) (domain.Task, bool, error) {
	var t domain.Task
	var reasons []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, ad_id, user_id, title, content_hash, status, auto_result, reasons,
		       COALESCE(decided_by::text, ''), COALESCE(comment, ''), decided_at, created_at, updated_at
		FROM moderation_tasks WHERE ad_id = $1
	`, adID).Scan(&t.ID, &t.AdID, &t.UserID, &t.Title, &t.ContentHash, &t.Status, &t.AutoResult,
		&reasons, &t.DecidedBy, &t.Comment, &t.DecidedAt, &t.CreatedAt, &t.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Task{}, false, nil
	case err != nil:
		return domain.Task{}, false, fmt.Errorf("repo: чтение задачи %s: %w", adID, err)
	}
	if err := json.Unmarshal(reasons, &t.Reasons); err != nil {
		return domain.Task{}, false, fmt.Errorf("repo: разбор причин: %w", err)
	}
	return t, true, nil
}

// DecideWithEvent применяет ручное решение модератора к задаче в статусе manual
// (условный UPDATE защищает от гонок) и публикует событие для Listing/Notification
// одной транзакцией. applied=false, если задача уже не в статусе manual.
func (r *Repo) DecideWithEvent(ctx context.Context, adID, newStatus, moderatorID, comment string, ev events.Envelope) (bool, error) {
	applied := false
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE moderation_tasks
			SET status = $2, decided_by = $3, comment = $4, decided_at = now(), updated_at = now()
			WHERE ad_id = $1 AND status = $5
		`, adID, newStatus, moderatorID, comment, domain.StatusManual)
		if err != nil {
			return fmt.Errorf("repo: применение решения к %s: %w", adID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil // задача уже вне ручной очереди
		}
		applied = true
		return outbox.Enqueue(ctx, tx, ev)
	})
	return applied, err
}

// ListTasks возвращает задачи по статусу (для очереди/аудита) в порядке поступления.
func (r *Repo) ListTasks(ctx context.Context, status string, limit int) ([]domain.Task, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, ad_id, user_id, title, content_hash, status, auto_result, reasons, created_at, updated_at
		FROM moderation_tasks
		WHERE status = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: список задач: %w", err)
	}
	defer rows.Close()

	var out []domain.Task
	for rows.Next() {
		var t domain.Task
		var reasons []byte
		if err := rows.Scan(&t.ID, &t.AdID, &t.UserID, &t.Title, &t.ContentHash,
			&t.Status, &t.AutoResult, &reasons, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repo: скан задачи: %w", err)
		}
		if err := json.Unmarshal(reasons, &t.Reasons); err != nil {
			return nil, fmt.Errorf("repo: разбор причин: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: обход задач: %w", err)
	}
	return out, nil
}
