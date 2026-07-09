// Package repo содержит репозитории Listing-сервиса (PostgreSQL через pgx).
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

	"bozor/services/listing/internal/domain"
)

const adColumns = `id, user_id, category_id, title, description, price, currency,
	region_id, city_id, lat, lng, status, phone_display, published_at,
	expires_at, bumped_at, views_count, created_at, updated_at`

// Repo — репозиторий объявлений.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// CreateWithEvent вставляет объявление вместе со значениями атрибутов и
// изображениями, кладёт событие в outbox — всё одной транзакцией.
func (r *Repo) CreateWithEvent(ctx context.Context, a domain.Ad, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO ads (id, user_id, category_id, title, description, price, currency,
				region_id, city_id, lat, lng, status, phone_display, published_at,
				expires_at, bumped_at, views_count, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		`, a.ID, a.UserID, a.CategoryID, a.Title, a.Description, a.Price, a.Currency,
			a.RegionID, a.CityID, a.Lat, a.Lng, string(a.Status), a.PhoneDisplay, a.PublishedAt,
			a.ExpiresAt, a.BumpedAt, a.ViewsCount, a.CreatedAt, a.UpdatedAt)
		if err != nil {
			return fmt.Errorf("repo: вставка объявления: %w", err)
		}
		if err := insertAttributes(ctx, tx, a.ID, a.Attributes); err != nil {
			return err
		}
		if err := insertImages(ctx, tx, a.ID, a.Images); err != nil {
			return err
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// insertAttributes вставляет значения атрибутов объявления.
func insertAttributes(ctx context.Context, tx pgx.Tx, adID string, values []domain.AdAttributeValue) error {
	for _, v := range values {
		_, err := tx.Exec(ctx,
			`INSERT INTO ad_attribute_values (ad_id, attribute_slug, value) VALUES ($1,$2,$3)`,
			adID, v.AttributeSlug, v.Value)
		if err != nil {
			return fmt.Errorf("repo: вставка значения атрибута %q: %w", v.AttributeSlug, err)
		}
	}
	return nil
}

// insertImages вставляет привязки изображений объявления.
func insertImages(ctx context.Context, tx pgx.Tx, adID string, images []domain.AdImage) error {
	for _, img := range images {
		_, err := tx.Exec(ctx,
			`INSERT INTO ad_images (ad_id, media_id, sort_order, is_cover) VALUES ($1,$2,$3,$4)`,
			adID, img.MediaID, img.SortOrder, img.IsCover)
		if err != nil {
			return fmt.Errorf("repo: вставка изображения %q: %w", img.MediaID, err)
		}
	}
	return nil
}

// GetByID возвращает объявление со значениями атрибутов и изображениями.
func (r *Repo) GetByID(ctx context.Context, id string) (domain.Ad, error) {
	a, err := scanAd(r.pool.QueryRow(ctx, `SELECT `+adColumns+` FROM ads WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Ad{}, domain.ErrAdNotFound
	}
	if err != nil {
		return domain.Ad{}, fmt.Errorf("repo: запрос объявления: %w", err)
	}
	if a.Attributes, err = r.attributes(ctx, id); err != nil {
		return domain.Ad{}, err
	}
	if a.Images, err = r.images(ctx, id); err != nil {
		return domain.Ad{}, err
	}
	return a, nil
}

// attributes читает значения атрибутов объявления.
func (r *Repo) attributes(ctx context.Context, adID string) ([]domain.AdAttributeValue, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT attribute_slug, value FROM ad_attribute_values WHERE ad_id = $1 ORDER BY attribute_slug`, adID)
	if err != nil {
		return nil, fmt.Errorf("repo: запрос атрибутов: %w", err)
	}
	defer rows.Close()

	var out []domain.AdAttributeValue
	for rows.Next() {
		var v domain.AdAttributeValue
		if err := rows.Scan(&v.AttributeSlug, &v.Value); err != nil {
			return nil, fmt.Errorf("repo: чтение атрибута: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// images читает привязки изображений объявления (обложка/порядок).
func (r *Repo) images(ctx context.Context, adID string) ([]domain.AdImage, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT media_id, sort_order, is_cover FROM ad_images WHERE ad_id = $1 ORDER BY sort_order, media_id`, adID)
	if err != nil {
		return nil, fmt.Errorf("repo: запрос изображений: %w", err)
	}
	defer rows.Close()

	var out []domain.AdImage
	for rows.Next() {
		var img domain.AdImage
		if err := rows.Scan(&img.MediaID, &img.SortOrder, &img.IsCover); err != nil {
			return nil, fmt.Errorf("repo: чтение изображения: %w", err)
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

// scanAd читает строку объявления.
func scanAd(row pgx.Row) (domain.Ad, error) {
	var (
		a      domain.Ad
		status string
	)
	err := row.Scan(&a.ID, &a.UserID, &a.CategoryID, &a.Title, &a.Description, &a.Price, &a.Currency,
		&a.RegionID, &a.CityID, &a.Lat, &a.Lng, &status, &a.PhoneDisplay, &a.PublishedAt,
		&a.ExpiresAt, &a.BumpedAt, &a.ViewsCount, &a.CreatedAt, &a.UpdatedAt)
	a.Status = domain.Status(status)
	return a, err
}
