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
