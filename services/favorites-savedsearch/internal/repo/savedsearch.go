package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/outbox"
	"bozor/pkg/shared/pgxx"

	"bozor/services/favorites-savedsearch/internal/domain"
)

const savedSearchColumns = `id, user_id, name, query_json, notify_enabled, last_notified_at, created_at`

// CreateSavedSearch вставляет сохранённый поиск. Грубые ключи category_id/
// region_id извлекаются из запроса для селективного отбора кандидатов (NULL = любой).
func (r *Repo) CreateSavedSearch(ctx context.Context, ss domain.SavedSearch) error {
	queryJSON, err := json.Marshal(ss.Query)
	if err != nil {
		return fmt.Errorf("repo: сериализация запроса: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO saved_searches (id, user_id, name, query_json, category_id, region_id, notify_enabled, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, ss.ID, ss.UserID, ss.Name, queryJSON,
		coarseCategory(ss.Query.CategoryID), coarseRegion(ss.Query.RegionID), ss.NotifyEnabled, ss.CreatedAt)
	if err != nil {
		return fmt.Errorf("repo: вставка сохранённого поиска: %w", err)
	}
	return nil
}

// CountSavedSearches возвращает число сохранённых поисков пользователя (для лимита).
func (r *Repo) CountSavedSearches(ctx context.Context, userID string) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM saved_searches WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: подсчёт сохранённых поисков: %w", err)
	}
	return n, nil
}

// ListSavedSearches возвращает сохранённые поиски пользователя (свежие сверху).
func (r *Repo) ListSavedSearches(ctx context.Context, userID string) ([]domain.SavedSearch, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+savedSearchColumns+` FROM saved_searches WHERE user_id = $1 ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("repo: чтение сохранённых поисков: %w", err)
	}
	defer rows.Close()
	return scanSavedSearches(rows)
}

// DeleteSavedSearch удаляет сохранённый поиск владельца. Возвращает признак существования.
func (r *Repo) DeleteSavedSearch(ctx context.Context, id, userID string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM saved_searches WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, fmt.Errorf("repo: удаление сохранённого поиска: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Candidates возвращает сохранённые поиски-кандидаты по грубым ключам объявления
// (category/region совпадают или не заданы) с включёнными уведомлениями. Тонкую
// оценку (цена/город/атрибуты/текст) выполняет приложение.
func (r *Repo) Candidates(ctx context.Context, categoryID string, regionID int16) ([]domain.SavedSearch, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+savedSearchColumns+` FROM saved_searches
		WHERE notify_enabled
		  AND (category_id IS NULL OR category_id = $1)
		  AND (region_id IS NULL OR region_id = $2)
	`, coarseCategory(categoryID), coarseRegion(regionID))
	if err != nil {
		return nil, fmt.Errorf("repo: отбор кандидатов: %w", err)
	}
	defer rows.Close()
	return scanSavedSearches(rows)
}

// RecordMatchWithEvent атомарно фиксирует совпадение (дедуп пары поиск/объявление)
// и, если троттлинг позволяет, публикует событие через outbox и обновляет
// last_notified_at. Возвращает признак публикации. Идемпотентно: повторное
// совпадение той же пары не публикуется (ON CONFLICT DO NOTHING).
func (r *Repo) RecordMatchWithEvent(ctx context.Context, searchID, adID string, throttleSeconds int, ev events.Envelope) (bool, error) {
	published := false
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO saved_search_matches (saved_search_id, ad_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			searchID, adID)
		if err != nil {
			return fmt.Errorf("repo: дедуп совпадения: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil // пара уже уведомлена
		}
		// Троттлинг на сохранённый поиск: обновляем last_notified_at только если
		// прошло достаточно времени; иначе подавляем публикацию (защита от лавины).
		utag, err := tx.Exec(ctx, `
			UPDATE saved_searches SET last_notified_at = now()
			WHERE id = $1 AND (last_notified_at IS NULL OR last_notified_at <= now() - ($2 * interval '1 second'))
		`, searchID, throttleSeconds)
		if err != nil {
			return fmt.Errorf("repo: троттлинг уведомления: %w", err)
		}
		if utag.RowsAffected() == 0 {
			return nil // подавлено троттлингом (дедуп записан)
		}
		if err := outbox.Enqueue(ctx, tx, ev); err != nil {
			return err
		}
		published = true
		return nil
	})
	return published, err
}

// coarseCategory нормализует грубый ключ категории (пусто → NULL = любой).
func coarseCategory(categoryID string) *string {
	if categoryID == "" {
		return nil
	}
	return &categoryID
}

// coarseRegion нормализует грубый ключ региона (0 → NULL = любой).
func coarseRegion(regionID int16) *int16 {
	if regionID == 0 {
		return nil
	}
	return &regionID
}

// scanSavedSearches читает набор сохранённых поисков.
func scanSavedSearches(rows pgx.Rows) ([]domain.SavedSearch, error) {
	var out []domain.SavedSearch
	for rows.Next() {
		ss, err := scanSavedSearch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: итерация сохранённых поисков: %w", err)
	}
	return out, nil
}

// scanSavedSearch читает один сохранённый поиск (разбирая query_json).
func scanSavedSearch(row pgx.Row) (domain.SavedSearch, error) {
	var ss domain.SavedSearch
	var queryJSON []byte
	err := row.Scan(&ss.ID, &ss.UserID, &ss.Name, &queryJSON, &ss.NotifyEnabled, &ss.LastNotifiedAt, &ss.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.SavedSearch{}, domain.ErrSavedSearchNotFound
		}
		return domain.SavedSearch{}, fmt.Errorf("repo: разбор сохранённого поиска: %w", err)
	}
	if err := json.Unmarshal(queryJSON, &ss.Query); err != nil {
		return domain.SavedSearch{}, fmt.Errorf("repo: разбор query_json: %w", err)
	}
	return ss, nil
}
