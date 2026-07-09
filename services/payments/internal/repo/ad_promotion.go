package repo

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/payments/internal/domain"
)

// ActivatePromotions создаёт строки применённых услуг и кладёт событие активации
// (bozor.promotion.activated) в outbox — всё одной транзакцией. Это шаг «активация»
// саги покупки: вызывается после успешного списания с кошелька.
func (r *Repo) ActivatePromotions(ctx context.Context, promos []domain.AdPromotion, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		for _, p := range promos {
			var schedule []byte
			if len(p.Schedule) > 0 {
				b, err := json.Marshal(p.Schedule)
				if err != nil {
					return err
				}
				schedule = b
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO ad_promotions
				  (id, ad_id, user_id, service_code, bundle_code, status, amount_uzs, starts_at, ends_at, schedule_json)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				p.ID, p.AdID, p.UserID, p.ServiceCode, p.BundleCode, p.Status, p.AmountUZS,
				p.StartsAt, p.EndsAt, schedule); err != nil {
				return err
			}
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// DueBumps возвращает созревшие дни авто-поднятия: развёртывает расписание
// schedule_json активных услуг и оставляет дни, у которых наступил момент
// (starts_at + day*24ч ≤ now) и которые ещё не исполнены (нет в bump_runs).
// Порядок — по времени старта (старейшие первыми), не более limit строк.
func (r *Repo) DueBumps(ctx context.Context, limit int) ([]domain.DueBump, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.ad_id, elem::int AS day_offset
		FROM ad_promotions p,
		     LATERAL jsonb_array_elements_text(p.schedule_json) AS elem
		WHERE p.status = 'active'
		  AND p.schedule_json IS NOT NULL
		  AND p.starts_at + (elem::int) * interval '1 day' <= now()
		  AND NOT EXISTS (
		      SELECT 1 FROM bump_runs r
		      WHERE r.promotion_id = p.id AND r.day_offset = elem::int
		  )
		ORDER BY p.starts_at
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.DueBump
	for rows.Next() {
		var d domain.DueBump
		if err := rows.Scan(&d.PromotionID, &d.AdID, &d.DayOffset); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClaimBump атомарно резервирует день поднятия за воркером: вставляет строку в
// bump_runs. Возвращает true, если запись создана (день застолблён этим вызовом),
// false — если уже была (гонка/повтор): такой день пропускается без поднятия.
func (r *Repo) ClaimBump(ctx context.Context, promotionID string, dayOffset int) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO bump_runs (promotion_id, day_offset) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, promotionID, dayOffset)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseBump снимает резерв дня поднятия (после неуспешного поднятия в Listing),
// чтобы следующий тик повторил попытку. Best-effort: ошибка лишь логируется.
func (r *Repo) ReleaseBump(ctx context.Context, promotionID string, dayOffset int) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM bump_runs WHERE promotion_id = $1 AND day_offset = $2`, promotionID, dayOffset)
	return err
}

// ListAdPromotions возвращает услуги объявления указанного статуса (свежие сверху).
func (r *Repo) ListAdPromotions(ctx context.Context, adID, status string) ([]domain.AdPromotion, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, ad_id, user_id, service_code, bundle_code, status, amount_uzs,
		       starts_at, ends_at, schedule_json, created_at
		FROM ad_promotions
		WHERE ad_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC`, adID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.AdPromotion
	for rows.Next() {
		var (
			p        domain.AdPromotion
			schedule []byte
		)
		if err := rows.Scan(&p.ID, &p.AdID, &p.UserID, &p.ServiceCode, &p.BundleCode, &p.Status,
			&p.AmountUZS, &p.StartsAt, &p.EndsAt, &schedule, &p.CreatedAt); err != nil {
			return nil, err
		}
		if len(schedule) > 0 {
			if err := json.Unmarshal(schedule, &p.Schedule); err != nil {
				return nil, err
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
