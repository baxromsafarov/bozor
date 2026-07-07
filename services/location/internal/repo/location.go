// Package repo содержит репозитории Location-сервиса (PostgreSQL через pgx).
package repo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/services/location/internal/domain"
)

// Repo — репозиторий справочника регионов и городов.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Regions возвращает все регионы, упорядоченные для показа.
func (r *Repo) Regions(ctx context.Context) ([]domain.Region, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, slug, name_uz, name_ru, latitude, longitude, sort_order
		FROM regions
		ORDER BY sort_order, id
	`)
	if err != nil {
		return nil, fmt.Errorf("repo: запрос регионов: %w", err)
	}
	defer rows.Close()

	var out []domain.Region
	for rows.Next() {
		var reg domain.Region
		if err := rows.Scan(&reg.ID, &reg.Slug, &reg.NameUZ, &reg.NameRU,
			&reg.Latitude, &reg.Longitude, &reg.SortOrder); err != nil {
			return nil, fmt.Errorf("repo: скан региона: %w", err)
		}
		out = append(out, reg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: итерация регионов: %w", err)
	}
	return out, nil
}

// CitiesByRegion возвращает города региона, упорядоченные для показа.
func (r *Repo) CitiesByRegion(ctx context.Context, regionID int) ([]domain.City, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, region_id, slug, name_uz, name_ru, latitude, longitude, sort_order
		FROM cities
		WHERE region_id = $1
		ORDER BY sort_order, id
	`, regionID)
	if err != nil {
		return nil, fmt.Errorf("repo: запрос городов: %w", err)
	}
	defer rows.Close()

	var out []domain.City
	for rows.Next() {
		var c domain.City
		if err := rows.Scan(&c.ID, &c.RegionID, &c.Slug, &c.NameUZ, &c.NameRU,
			&c.Latitude, &c.Longitude, &c.SortOrder); err != nil {
			return nil, fmt.Errorf("repo: скан города: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: итерация городов: %w", err)
	}
	return out, nil
}

// RegionExists сообщает, существует ли регион с указанным id (для 404 vs пустой).
func (r *Repo) RegionExists(ctx context.Context, regionID int) (bool, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM regions WHERE id = $1)`, regionID).Scan(&exists); err != nil {
		return false, fmt.Errorf("repo: проверка региона: %w", err)
	}
	return exists, nil
}
