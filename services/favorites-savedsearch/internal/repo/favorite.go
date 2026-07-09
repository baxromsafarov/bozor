// Package repo содержит репозитории Favorites/SavedSearch-сервиса (PostgreSQL через pgx).
package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/services/favorites-savedsearch/internal/domain"
)

// Repo — репозиторий избранного.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Add добавляет объявление в избранное пользователя (идемпотентно) и возвращает
// время добавления: при повторе сохраняется исходный created_at.
func (r *Repo) Add(ctx context.Context, userID, adID string) (time.Time, error) {
	var createdAt time.Time
	err := r.pool.QueryRow(ctx, `
		INSERT INTO favorites (user_id, ad_id) VALUES ($1, $2)
		ON CONFLICT (user_id, ad_id) DO UPDATE SET created_at = favorites.created_at
		RETURNING created_at
	`, userID, adID).Scan(&createdAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("repo: добавление в избранное: %w", err)
	}
	return createdAt, nil
}

// Remove удаляет объявление из избранного пользователя. Идемпотентно: возвращает
// признак того, что запись существовала.
func (r *Repo) Remove(ctx context.Context, userID, adID string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM favorites WHERE user_id = $1 AND ad_id = $2`, userID, adID)
	if err != nil {
		return false, fmt.Errorf("repo: удаление из избранного: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListByUser возвращает избранное пользователя (свежие сверху, пагинация).
func (r *Repo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]domain.Favorite, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, ad_id, created_at FROM favorites
		WHERE user_id = $1 ORDER BY created_at DESC, ad_id LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("repo: чтение избранного: %w", err)
	}
	defer rows.Close()

	var favs []domain.Favorite
	for rows.Next() {
		var f domain.Favorite
		if err := rows.Scan(&f.UserID, &f.AdID, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("repo: разбор избранного: %w", err)
		}
		favs = append(favs, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: итерация избранного: %w", err)
	}
	return favs, nil
}

// RemoveByAd удаляет все записи избранного по объявлению (очистка при
// bozor.ad.deleted). Возвращает число удалённых строк.
func (r *Repo) RemoveByAd(ctx context.Context, adID string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM favorites WHERE ad_id = $1`, adID)
	if err != nil {
		return 0, fmt.Errorf("repo: очистка избранного по объявлению: %w", err)
	}
	return tag.RowsAffected(), nil
}
