package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/moderation/internal/domain"
)

// CreateReportWithEvent создаёт жалобу и публикует bozor.moderation.report_created
// одной транзакцией.
func (r *Repo) CreateReportWithEvent(ctx context.Context, rep domain.Report, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO reports (id, reporter_id, target_type, target_id, reason, status)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, rep.ID, rep.ReporterID, rep.TargetType, rep.TargetID, rep.Reason, domain.ReportOpen)
		if err != nil {
			return fmt.Errorf("repo: создание жалобы: %w", err)
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// GetReport возвращает жалобу по id. found=false, если жалобы нет.
func (r *Repo) GetReport(ctx context.Context, id string) (domain.Report, bool, error) {
	var rep domain.Report
	err := r.pool.QueryRow(ctx, `
		SELECT id, reporter_id, target_type, target_id, reason, status,
		       COALESCE(resolution, ''), COALESCE(resolved_by::text, ''), resolved_at, created_at
		FROM reports WHERE id = $1
	`, id).Scan(&rep.ID, &rep.ReporterID, &rep.TargetType, &rep.TargetID, &rep.Reason,
		&rep.Status, &rep.Resolution, &rep.ResolvedBy, &rep.ResolvedAt, &rep.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Report{}, false, nil
	case err != nil:
		return domain.Report{}, false, fmt.Errorf("repo: чтение жалобы %s: %w", id, err)
	}
	return rep, true, nil
}

// ResolveReportWithEvent закрывает открытую жалобу (условный UPDATE WHERE status=open —
// защита от гонок) и, если задано, публикует событие (напр. снятие объявления).
// applied=false, если жалоба уже закрыта.
func (r *Repo) ResolveReportWithEvent(ctx context.Context, reportID, status, resolution, moderatorID string, ev *events.Envelope) (bool, error) {
	applied := false
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE reports
			SET status = $2, resolution = $3, resolved_by = $4, resolved_at = now()
			WHERE id = $1 AND status = $5
		`, reportID, status, resolution, moderatorID, domain.ReportOpen)
		if err != nil {
			return fmt.Errorf("repo: закрытие жалобы %s: %w", reportID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		applied = true
		if ev != nil {
			return outbox.Enqueue(ctx, tx, *ev)
		}
		return nil
	})
	return applied, err
}

// ListReports возвращает жалобы по статусу (очередь) в порядке поступления.
func (r *Repo) ListReports(ctx context.Context, status string, limit int) ([]domain.Report, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, reporter_id, target_type, target_id, reason, status,
		       COALESCE(resolution, ''), COALESCE(resolved_by::text, ''), resolved_at, created_at
		FROM reports WHERE status = $1 ORDER BY created_at DESC LIMIT $2
	`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: список жалоб: %w", err)
	}
	defer rows.Close()

	var out []domain.Report
	for rows.Next() {
		var rep domain.Report
		if err := rows.Scan(&rep.ID, &rep.ReporterID, &rep.TargetType, &rep.TargetID, &rep.Reason,
			&rep.Status, &rep.Resolution, &rep.ResolvedBy, &rep.ResolvedAt, &rep.CreatedAt); err != nil {
			return nil, fmt.Errorf("repo: скан жалобы: %w", err)
		}
		out = append(out, rep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: обход жалоб: %w", err)
	}
	return out, nil
}

// CreateBanWithEvent создаёт бан и публикует bozor.user.banned одной транзакцией.
func (r *Repo) CreateBanWithEvent(ctx context.Context, ban domain.Ban, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO bans (id, user_id, type, reason, expires_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, ban.ID, ban.UserID, ban.Type, ban.Reason, ban.ExpiresAt, ban.CreatedBy)
		if err != nil {
			return fmt.Errorf("repo: создание бана: %w", err)
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}
