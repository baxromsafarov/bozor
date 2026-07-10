// Package repo содержит репозитории User/Profile-сервиса (PostgreSQL через pgx).
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

	"bozor/services/user-profile/internal/domain"
)

const profileColumns = `user_id, display_name, avatar_media_id, about, user_type,
	business_name, city_id, contact_phone_visible, language_code, created_at, updated_at`

// Repo — репозиторий профилей.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo создаёт репозиторий поверх пула соединений.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// insertProfile — идемпотентная вставка профиля (ON CONFLICT DO NOTHING).
const insertProfile = `
	INSERT INTO profiles (user_id, user_type, contact_phone_visible, language_code, created_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (user_id) DO NOTHING`

// EnsureProfile идемпотентно создаёт профиль по умолчанию (лениво при первом
// обращении к /me, если событие ещё не обработано). Существующий не меняется.
func (r *Repo) EnsureProfile(ctx context.Context, p domain.Profile) error {
	_, err := r.pool.Exec(ctx, insertProfile,
		p.UserID, string(p.UserType), p.ContactPhoneVisible, p.LanguageCode, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("repo: создание профиля: %w", err)
	}
	return nil
}

// CreateProfileWithInbox создаёт профиль по умолчанию и отмечает событие
// обработанным (inbox) одной транзакцией — обработка bozor.user.created «ровно
// один раз». Вставка идемпотентна (ON CONFLICT DO NOTHING).
func (r *Repo) CreateProfileWithInbox(ctx context.Context, p domain.Profile, consumer, eventID string) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insertProfile,
			p.UserID, string(p.UserType), p.ContactPhoneVisible, p.LanguageCode, p.CreatedAt, p.UpdatedAt)
		if err != nil {
			return fmt.Errorf("repo: создание профиля: %w", err)
		}
		return outbox.MarkProcessed(ctx, tx, consumer, eventID)
	})
}

// GetProfile возвращает профиль по user_id (ErrProfileNotFound, если нет).
func (r *Repo) GetProfile(ctx context.Context, userID string) (domain.Profile, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+profileColumns+` FROM profiles WHERE user_id = $1`, userID)
	p, err := scanProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Profile{}, domain.ErrProfileNotFound
	}
	if err != nil {
		return domain.Profile{}, fmt.Errorf("repo: чтение профиля: %w", err)
	}
	return p, nil
}

// UpdateProfileWithEvent обновляет изменяемые поля профиля и кладёт событие
// bozor.user.updated в outbox одной транзакцией. ErrProfileNotFound, если строки нет.
func (r *Repo) UpdateProfileWithEvent(ctx context.Context, p domain.Profile, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE profiles SET
				display_name = $2, avatar_media_id = $3, about = $4, user_type = $5,
				business_name = $6, city_id = $7, contact_phone_visible = $8, updated_at = now()
			WHERE user_id = $1
		`, p.UserID, p.DisplayName, p.AvatarMediaID, p.About, string(p.UserType),
			p.BusinessName, p.CityID, p.ContactPhoneVisible)
		if err != nil {
			return fmt.Errorf("repo: обновление профиля: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrProfileNotFound
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// UpsertRatingWithInbox обновляет кеш рейтинга продавца и отмечает событие
// bozor.review.created обработанным (inbox) одной транзакцией. Значения —
// пересчитанный агрегат из Reviews (fetch-current-state). Профиль продавца при
// необходимости создаётся по умолчанию (FK user_ratings_cache → profiles;
// продавец мог ещё не открывать /me). Идемпотентно: UPSERT к текущему агрегату +
// MarkProcessed; повторная доставка отсекается inbox (проверяется до вызова).
func (r *Repo) UpsertRatingWithInbox(ctx context.Context, def domain.Profile, rt domain.Rating, consumer, eventID string) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, insertProfile,
			def.UserID, string(def.UserType), def.ContactPhoneVisible, def.LanguageCode, def.CreatedAt, def.UpdatedAt); err != nil {
			return fmt.Errorf("repo: создание профиля продавца: %w", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO user_ratings_cache (user_id, avg_rating, reviews_count, updated_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (user_id) DO UPDATE
			SET avg_rating = excluded.avg_rating, reviews_count = excluded.reviews_count, updated_at = now()`,
			def.UserID, rt.AvgRating, rt.ReviewsCount)
		if err != nil {
			return fmt.Errorf("repo: обновление кеша рейтинга: %w", err)
		}
		return outbox.MarkProcessed(ctx, tx, consumer, eventID)
	})
}

// GetRating возвращает кеш рейтинга (нулевой при отсутствии — отзывов ещё нет).
func (r *Repo) GetRating(ctx context.Context, userID string) (domain.Rating, error) {
	var rt domain.Rating
	err := r.pool.QueryRow(ctx,
		`SELECT avg_rating, reviews_count FROM user_ratings_cache WHERE user_id = $1`, userID).
		Scan(&rt.AvgRating, &rt.ReviewsCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Rating{}, nil
	}
	if err != nil {
		return domain.Rating{}, fmt.Errorf("repo: чтение рейтинга: %w", err)
	}
	return rt, nil
}

// GetNotificationPrefs возвращает сохранённые настройки уведомлений пользователя.
func (r *Repo) GetNotificationPrefs(ctx context.Context, userID string) ([]domain.NotificationPref, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT channel, event_type, enabled FROM notification_prefs WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("repo: чтение настроек уведомлений: %w", err)
	}
	defer rows.Close()

	var prefs []domain.NotificationPref
	for rows.Next() {
		var p domain.NotificationPref
		if err := rows.Scan(&p.Channel, &p.EventType, &p.Enabled); err != nil {
			return nil, fmt.Errorf("repo: разбор настройки уведомления: %w", err)
		}
		prefs = append(prefs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: итерация настроек уведомлений: %w", err)
	}
	return prefs, nil
}

// ReplaceNotificationPrefs заменяет весь набор настроек пользователя одной
// транзакцией (удаление + вставка). Пустой prefs очищает набор (вернётся дефолт).
func (r *Repo) ReplaceNotificationPrefs(ctx context.Context, userID string, prefs []domain.NotificationPref) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM notification_prefs WHERE user_id = $1`, userID); err != nil {
			return fmt.Errorf("repo: очистка настроек уведомлений: %w", err)
		}
		for _, p := range prefs {
			_, err := tx.Exec(ctx,
				`INSERT INTO notification_prefs (user_id, channel, event_type, enabled) VALUES ($1,$2,$3,$4)`,
				userID, p.Channel, p.EventType, p.Enabled)
			if err != nil {
				return fmt.Errorf("repo: вставка настройки уведомления: %w", err)
			}
		}
		return nil
	})
}

// IsEventProcessed сообщает, обрабатывал ли consumer событие eventID (inbox).
func (r *Repo) IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error) {
	return outbox.AlreadyProcessed(ctx, r.pool, consumer, eventID)
}

// MarkEventProcessed помечает событие обработанным без иных изменений.
func (r *Repo) MarkEventProcessed(ctx context.Context, consumer, eventID string) error {
	return outbox.MarkProcessed(ctx, r.pool, consumer, eventID)
}

// scanProfile читает профиль из строки результата.
func scanProfile(row pgx.Row) (domain.Profile, error) {
	var p domain.Profile
	var userType string
	err := row.Scan(&p.UserID, &p.DisplayName, &p.AvatarMediaID, &p.About, &userType,
		&p.BusinessName, &p.CityID, &p.ContactPhoneVisible, &p.LanguageCode, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return domain.Profile{}, err
	}
	p.UserType = domain.UserType(userType)
	return p, nil
}
