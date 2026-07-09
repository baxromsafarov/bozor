// Package repo — доступ к БД каталога платных услуг (Stage 8.1). Читает каталог
// услуг/наборов и правила ценообразования из bozor_payments через pgx.
package repo

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/services/payments/internal/domain"
)

// Repo — репозиторий каталога поверх пула pgx.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий каталога.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Services возвращает активные услуги в порядке отображения.
func (r *Repo) Services(ctx context.Context) ([]domain.Service, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT code, name_uz, name_ru, description_uz, description_ru, durations_days, sort_order
		FROM promotion_services
		WHERE is_active
		ORDER BY sort_order, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Service
	for rows.Next() {
		var s domain.Service
		var durs []int32
		if err := rows.Scan(&s.Code, &s.NameUZ, &s.NameRU, &s.DescriptionUZ, &s.DescriptionRU,
			&durs, &s.SortOrder); err != nil {
			return nil, err
		}
		s.Durations = toInts(durs)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Bundles возвращает активные наборы с их составом (два запроса, склейка в Go).
func (r *Repo) Bundles(ctx context.Context) ([]domain.Bundle, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT code, name_uz, name_ru, description_uz, description_ru, sort_order
		FROM promotion_bundles
		WHERE is_active
		ORDER BY sort_order, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bundles := make([]domain.Bundle, 0)
	idx := make(map[string]int)
	for rows.Next() {
		var b domain.Bundle
		if err := rows.Scan(&b.Code, &b.NameUZ, &b.NameRU, &b.DescriptionUZ, &b.DescriptionRU,
			&b.SortOrder); err != nil {
			return nil, err
		}
		idx[b.Code] = len(bundles)
		bundles = append(bundles, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	itemRows, err := r.pool.Query(ctx, `
		SELECT bundle_code, service_code, duration_days, bump_schedule_days, sort_order
		FROM promotion_bundle_items
		ORDER BY bundle_code, sort_order`)
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()

	for itemRows.Next() {
		var (
			bundleCode string
			it         domain.BundleItem
			sched      []int32
		)
		if err := itemRows.Scan(&bundleCode, &it.ServiceCode, &it.Duration, &sched, &it.SortOrder); err != nil {
			return nil, err
		}
		it.BumpSchedule = toInts(sched)
		if i, ok := idx[bundleCode]; ok {
			bundles[i].Items = append(bundles[i].Items, it)
		}
	}
	return bundles, itemRows.Err()
}

// PriceRules возвращает правила цены, подходящие под регион и категорию: точное
// совпадение либо базовое (NULL). Приоритет (самое конкретное) выбирается в
// domain.ResolvePrices. nil-аргумент отбирает только базовые правила по этому
// измерению.
func (r *Repo) PriceRules(ctx context.Context, regionID *int, categoryID *string) ([]domain.PriceRule, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT product_type, product_code, region_id::bigint, category_id::text, duration_days, amount_uzs
		FROM pricing
		WHERE (region_id IS NULL OR region_id = $1::int)
		  AND (category_id IS NULL OR category_id = $2::uuid)`,
		regionID, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.PriceRule
	for rows.Next() {
		var (
			rule   domain.PriceRule
			region *int64
		)
		if err := rows.Scan(&rule.ProductType, &rule.ProductCode, &region, &rule.CategoryID,
			&rule.Duration, &rule.AmountUZS); err != nil {
			return nil, err
		}
		if region != nil {
			v := int(*region)
			rule.RegionID = &v
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// toInts преобразует массив int32 (как pgx читает integer[]) в []int.
func toInts(in []int32) []int {
	if len(in) == 0 {
		return nil
	}
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}
