// Package repo содержит репозиторий Notification-сервиса (PostgreSQL через pgx):
// проекцию получателей и журнал уведомлений со статусами доставки.
package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/services/notification/internal/domain"
)

// Repo — репозиторий уведомлений и получателей.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// UpsertRecipient сохраняет/обновляет проекцию получателя (из bozor.user.created).
// Идемпотентно: повторная доставка события не плодит записей.
func (r *Repo) UpsertRecipient(ctx context.Context, rec domain.Recipient) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO recipients (user_id, telegram_user_id, language_code, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id) DO UPDATE
		SET telegram_user_id = EXCLUDED.telegram_user_id,
		    language_code     = EXCLUDED.language_code,
		    updated_at        = now()
	`, rec.UserID, rec.TelegramUserID, rec.LanguageCode)
	if err != nil {
		return fmt.Errorf("repo: upsert получателя %s: %w", rec.UserID, err)
	}
	return nil
}

// GetRecipient возвращает проекцию получателя. found=false, если получатель
// неизвестен (событие bozor.user.created ещё не спроецировано или пользователь
// не регистрировался через Telegram).
func (r *Repo) GetRecipient(ctx context.Context, userID string) (domain.Recipient, bool, error) {
	var rec domain.Recipient
	err := r.pool.QueryRow(ctx,
		`SELECT user_id, telegram_user_id, language_code FROM recipients WHERE user_id = $1`,
		userID,
	).Scan(&rec.UserID, &rec.TelegramUserID, &rec.LanguageCode)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Recipient{}, false, nil
	case err != nil:
		return domain.Recipient{}, false, fmt.Errorf("repo: чтение получателя %s: %w", userID, err)
	}
	return rec, true, nil
}

// RecordSkipped записывает пропущенное уведомление (терминальный статус) —
// идемпотентно по event_id. Используется, когда доставка не нужна (настройки
// выключены, нет получателя, канал отключён).
func (r *Repo) RecordSkipped(ctx context.Context, id, eventID, userID, eventType string, payload []byte, reason string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notifications (id, event_id, user_id, event_type, channel, payload_json, status, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (event_id) DO NOTHING
	`, id, eventID, userID, eventType, domain.ChannelTelegram, payload, domain.StatusSkipped, reason)
	if err != nil {
		return fmt.Errorf("repo: запись skipped-уведомления %s: %w", eventID, err)
	}
	return nil
}

// BeginDelivery регистрирует попытку доставки: создаёт запись pending (attempts=1)
// либо, если запись уже есть, увеличивает attempts и обновляет текст. Возвращает
// статус записи ПОСЛЕ операции: proceed=true только для pending (иначе событие
// уже в терминальном статусе — доставку повторять не нужно).
func (r *Repo) BeginDelivery(ctx context.Context, id, eventID, userID, eventType string, payload []byte, body string) (proceed bool, err error) {
	var status string
	err = r.pool.QueryRow(ctx, `
		INSERT INTO notifications (id, event_id, user_id, event_type, channel, payload_json, body, status, attempts)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1)
		ON CONFLICT (event_id) DO UPDATE
		SET attempts = notifications.attempts + 1,
		    body     = EXCLUDED.body
		RETURNING status
	`, id, eventID, userID, eventType, domain.ChannelTelegram, payload, body, domain.StatusPending).Scan(&status)
	if err != nil {
		return false, fmt.Errorf("repo: регистрация доставки %s: %w", eventID, err)
	}
	return status == domain.StatusPending, nil
}

// MarkSent помечает уведомление доставленным.
func (r *Repo) MarkSent(ctx context.Context, eventID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE notifications SET status = $2, sent_at = now(), reason = NULL WHERE event_id = $1`,
		eventID, domain.StatusSent)
	if err != nil {
		return fmt.Errorf("repo: отметка sent %s: %w", eventID, err)
	}
	return nil
}

// MarkFailed помечает уведомление постоянно неуспешным (канал вернул неустранимую
// ошибку) с указанием причины.
func (r *Repo) MarkFailed(ctx context.Context, eventID, reason string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE notifications SET status = $2, reason = $3 WHERE event_id = $1`,
		eventID, domain.StatusFailed, reason)
	if err != nil {
		return fmt.Errorf("repo: отметка failed %s: %w", eventID, err)
	}
	return nil
}

// MarkSkipped переводит существующую запись в статус skipped с причиной
// (напр. канал отключён во время доставки).
func (r *Repo) MarkSkipped(ctx context.Context, eventID, reason string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE notifications SET status = $2, reason = $3 WHERE event_id = $1`,
		eventID, domain.StatusSkipped, reason)
	if err != nil {
		return fmt.Errorf("repo: отметка skipped %s: %w", eventID, err)
	}
	return nil
}

// ListByUser возвращает последние уведомления пользователя (история/статусы).
func (r *Repo) ListByUser(ctx context.Context, userID string, limit int) ([]domain.Notification, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, event_id, user_id, event_type, channel, body, status, COALESCE(reason, ''), attempts, created_at, sent_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: список уведомлений %s: %w", userID, err)
	}
	defer rows.Close()

	var out []domain.Notification
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(&n.ID, &n.EventID, &n.UserID, &n.EventType, &n.Channel,
			&n.Body, &n.Status, &n.Reason, &n.Attempts, &n.CreatedAt, &n.SentAt); err != nil {
			return nil, fmt.Errorf("repo: чтение строки уведомления: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: обход уведомлений: %w", err)
	}
	return out, nil
}
