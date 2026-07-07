package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"bozor/pkg/shared/pgxx"

	"bozor/services/catalog/internal/domain"
)

const attrColumns = `id, slug, name_uz, name_ru, type, unit, is_required, is_filterable`

// ListAttributes возвращает все атрибуты с вариантами (для enum), упорядоченные по slug.
func (r *Repo) ListAttributes(ctx context.Context) ([]domain.Attribute, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+attrColumns+` FROM attributes ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("repo: запрос атрибутов: %w", err)
	}
	attrs, err := scanAttributes(rows)
	if err != nil {
		return nil, err
	}
	if err := r.attachOptions(ctx, attrs); err != nil {
		return nil, err
	}
	return attrs, nil
}

// GetAttribute возвращает атрибут по id с вариантами (ErrAttributeNotFound если нет).
func (r *Repo) GetAttribute(ctx context.Context, id string) (domain.Attribute, error) {
	a, err := scanAttribute(r.pool.QueryRow(ctx, `SELECT `+attrColumns+` FROM attributes WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Attribute{}, domain.ErrAttributeNotFound
	}
	if err != nil {
		return domain.Attribute{}, fmt.Errorf("repo: запрос атрибута: %w", err)
	}
	list := []domain.Attribute{a}
	if err := r.attachOptions(ctx, list); err != nil {
		return domain.Attribute{}, err
	}
	return list[0], nil
}

// CreateAttribute вставляет атрибут и его варианты одной транзакцией.
func (r *Repo) CreateAttribute(ctx context.Context, a domain.Attribute) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO attributes (id, slug, name_uz, name_ru, type, unit, is_required, is_filterable)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, a.ID, a.Slug, a.NameUZ, a.NameRU, string(a.Type), a.Unit, a.IsRequired, a.IsFilterable)
		if isUniqueViolation(err) {
			return domain.ErrAttributeSlugConflict
		}
		if err != nil {
			return fmt.Errorf("repo: вставка атрибута: %w", err)
		}
		return insertOptions(ctx, tx, a.ID, a.Options)
	})
}

// UpdateAttribute обновляет поля атрибута и полностью заменяет набор вариантов.
func (r *Repo) UpdateAttribute(ctx context.Context, a domain.Attribute) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE attributes SET name_uz = $2, name_ru = $3, unit = $4,
				is_required = $5, is_filterable = $6
			WHERE id = $1
		`, a.ID, a.NameUZ, a.NameRU, a.Unit, a.IsRequired, a.IsFilterable)
		if err != nil {
			return fmt.Errorf("repo: обновление атрибута: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrAttributeNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM attribute_options WHERE attribute_id = $1`, a.ID); err != nil {
			return fmt.Errorf("repo: очистка вариантов: %w", err)
		}
		return insertOptions(ctx, tx, a.ID, a.Options)
	})
}

// DeleteAttribute удаляет атрибут (варианты и привязки уходят по ON DELETE CASCADE).
func (r *Repo) DeleteAttribute(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM attributes WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("repo: удаление атрибута: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAttributeNotFound
	}
	return nil
}

// EffectiveAttributes возвращает эффективные атрибуты категории: собственные и
// унаследованные от всех предков по материализованному path, сверху вниз.
// Дубли (атрибут привязан на нескольких уровнях) схлопываются к самому верхнему.
func (r *Repo) EffectiveAttributes(ctx context.Context, categoryID string) ([]domain.EffectiveAttribute, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.slug, a.name_uz, a.name_ru, a.type, a.unit, a.is_required, a.is_filterable,
		       ca.sort_order, anc.id, anc.level
		FROM categories t
		JOIN categories anc ON t.path = anc.path OR t.path LIKE anc.path || '/%'
		JOIN category_attributes ca ON ca.category_id = anc.id
		JOIN attributes a ON a.id = ca.attribute_id
		WHERE t.id = $1
		ORDER BY anc.level, ca.sort_order, a.slug
	`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("repo: запрос эффективных атрибутов: %w", err)
	}
	defer rows.Close()

	var out []domain.EffectiveAttribute
	seen := make(map[string]struct{})
	for rows.Next() {
		var (
			ea       domain.EffectiveAttribute
			typ      string
			sourceID string
			ancLevel int
		)
		if err := rows.Scan(&ea.ID, &ea.Slug, &ea.NameUZ, &ea.NameRU, &typ, &ea.Unit,
			&ea.IsRequired, &ea.IsFilterable, &ea.SortOrder, &sourceID, &ancLevel); err != nil {
			return nil, fmt.Errorf("repo: чтение эффективного атрибута: %w", err)
		}
		if _, dup := seen[ea.ID]; dup {
			continue
		}
		seen[ea.ID] = struct{}{}
		ea.Type = domain.AttributeType(typ)
		ea.SourceID = sourceID
		ea.Inherited = sourceID != categoryID
		out = append(out, ea)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: итерация эффективных атрибутов: %w", err)
	}

	attrs := make([]domain.Attribute, len(out))
	for i := range out {
		attrs[i] = out[i].Attribute
	}
	if err := r.attachOptions(ctx, attrs); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Options = attrs[i].Options
	}
	return out, nil
}

// LinkAttribute привязывает атрибут к категории.
func (r *Repo) LinkAttribute(ctx context.Context, categoryID, attributeID string, sortOrder int) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO category_attributes (category_id, attribute_id, sort_order)
		VALUES ($1, $2, $3)
	`, categoryID, attributeID, sortOrder)
	if isUniqueViolation(err) {
		return domain.ErrLinkExists
	}
	if err != nil {
		return fmt.Errorf("repo: привязка атрибута: %w", err)
	}
	return nil
}

// UnlinkAttribute снимает привязку атрибута с категории.
func (r *Repo) UnlinkAttribute(ctx context.Context, categoryID, attributeID string) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM category_attributes WHERE category_id = $1 AND attribute_id = $2
	`, categoryID, attributeID)
	if err != nil {
		return fmt.Errorf("repo: снятие привязки: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrLinkNotFound
	}
	return nil
}

// attachOptions догружает варианты для enum-атрибутов одним запросом.
func (r *Repo) attachOptions(ctx context.Context, attrs []domain.Attribute) error {
	ids := make([]string, 0, len(attrs))
	for i := range attrs {
		if attrs[i].Type == domain.TypeEnum {
			ids = append(ids, attrs[i].ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, attribute_id, slug, name_uz, name_ru, sort_order
		FROM attribute_options WHERE attribute_id = ANY($1)
		ORDER BY attribute_id, sort_order, slug
	`, ids)
	if err != nil {
		return fmt.Errorf("repo: запрос вариантов: %w", err)
	}
	defer rows.Close()

	byAttr := make(map[string][]domain.AttributeOption)
	for rows.Next() {
		var (
			o      domain.AttributeOption
			attrID string
		)
		if err := rows.Scan(&o.ID, &attrID, &o.Slug, &o.NameUZ, &o.NameRU, &o.SortOrder); err != nil {
			return fmt.Errorf("repo: чтение варианта: %w", err)
		}
		byAttr[attrID] = append(byAttr[attrID], o)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("repo: итерация вариантов: %w", err)
	}
	for i := range attrs {
		attrs[i].Options = byAttr[attrs[i].ID]
	}
	return nil
}

// insertOptions вставляет варианты enum-атрибута в рамках транзакции.
func insertOptions(ctx context.Context, tx pgx.Tx, attributeID string, opts []domain.AttributeOption) error {
	for _, o := range opts {
		_, err := tx.Exec(ctx, `
			INSERT INTO attribute_options (id, attribute_id, slug, name_uz, name_ru, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, o.ID, attributeID, o.Slug, o.NameUZ, o.NameRU, o.SortOrder)
		if isUniqueViolation(err) {
			return domain.ErrOptionSlugConflict
		}
		if err != nil {
			return fmt.Errorf("repo: вставка варианта: %w", err)
		}
	}
	return nil
}

// scanAttributes читает все строки атрибутов.
func scanAttributes(rows pgx.Rows) ([]domain.Attribute, error) {
	defer rows.Close()
	var out []domain.Attribute
	for rows.Next() {
		a, err := scanAttribute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: итерация атрибутов: %w", err)
	}
	return out, nil
}

// scanAttribute читает строку атрибута (без вариантов).
func scanAttribute(row pgx.Row) (domain.Attribute, error) {
	var (
		a   domain.Attribute
		typ string
	)
	err := row.Scan(&a.ID, &a.Slug, &a.NameUZ, &a.NameRU, &typ, &a.Unit, &a.IsRequired, &a.IsFilterable)
	a.Type = domain.AttributeType(typ)
	return a, err
}
