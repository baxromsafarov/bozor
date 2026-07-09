// Package repo содержит репозитории Listing-сервиса (PostgreSQL через pgx).
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

// execTransition выполняет условную смену статуса объявления внутри транзакции:
// UPDATE применяется только если текущий статус совпадает с upd.From (защита от
// гонок и переигранных событий). Nil-отметки времени сохраняются (COALESCE).
// Возвращает число затронутых строк.
func execTransition(ctx context.Context, tx pgx.Tx, adID string, upd domain.StatusUpdate) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE ads SET status = $2, updated_at = now(),
			published_at = COALESCE($3, published_at),
			expires_at   = COALESCE($4, expires_at)
		WHERE id = $1 AND status = $5
	`, adID, string(upd.To), upd.PublishedAt, upd.ExpiresAt, string(upd.From))
	if err != nil {
		return 0, fmt.Errorf("repo: смена статуса объявления: %w", err)
	}
	return tag.RowsAffected(), nil
}

// TransitionWithEvent атомарно меняет статус объявления (условно по upd.From) и
// кладёт событие в outbox. Если статус уже не upd.From (гонка/повтор) —
// ErrInvalidTransition, событие не публикуется. Используется действиями владельца
// (submit / sold / renew / archive), проверившими право и допустимость перехода.
func (r *Repo) TransitionWithEvent(ctx context.Context, adID string, upd domain.StatusUpdate, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		n, err := execTransition(ctx, tx, adID, upd)
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrInvalidTransition
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// ApplyModerationWithEvent применяет решение модерации к объявлению (условно по
// upd.From='pending'), публикует событие через outbox и отмечает событие
// обработанным (inbox) — всё в одной транзакции. Идемпотентно: повторная
// доставка не плодит событие (переход выполняется только из pending), но всё
// равно отмечает событие обработанным.
func (r *Repo) ApplyModerationWithEvent(ctx context.Context, consumer, eventID, adID string, upd domain.StatusUpdate, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		n, err := execTransition(ctx, tx, adID, upd)
		if err != nil {
			return err
		}
		if n > 0 {
			if err := outbox.Enqueue(ctx, tx, ev); err != nil {
				return err
			}
		}
		return outbox.MarkProcessed(ctx, tx, consumer, eventID)
	})
}

// IsEventProcessed сообщает, обрабатывал ли consumer событие eventID (inbox).
func (r *Repo) IsEventProcessed(ctx context.Context, consumer, eventID string) (bool, error) {
	return outbox.AlreadyProcessed(ctx, r.pool, consumer, eventID)
}

// MarkEventProcessed помечает событие обработанным без иных изменений (пропуск:
// объявление удалено или уже не в модерации — повторная обработка не нужна).
func (r *Repo) MarkEventProcessed(ctx context.Context, consumer, eventID string) error {
	return outbox.MarkProcessed(ctx, r.pool, consumer, eventID)
}

// ListExpired возвращает активные объявления с истёкшим сроком (expires_at <= now)
// — кандидаты на перевод в expired (не более limit за проход). Дочерние сущности
// не читаются: воркеру истечения нужны лишь поля события.
func (r *Repo) ListExpired(ctx context.Context, now time.Time, limit int) ([]domain.Ad, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+adColumns+`
		FROM ads WHERE status = 'active' AND expires_at IS NOT NULL AND expires_at <= $1
		ORDER BY expires_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: выборка истёкших объявлений: %w", err)
	}
	defer rows.Close()

	var out []domain.Ad
	for rows.Next() {
		a, err := scanAd(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: чтение истёкшего объявления: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: перебор истёкших объявлений: %w", err)
	}
	return out, nil
}

// ExpireWithEvent переводит объявление active → expired и публикует событие в
// outbox одной транзакцией. Условие expires_at <= now() перепроверяется в UPDATE:
// только что продлённое (renew) объявление не будет истечено гонкой. Возвращает
// true, если статус действительно сменился (иначе — пропуск без события).
func (r *Repo) ExpireWithEvent(ctx context.Context, adID string, ev events.Envelope) (bool, error) {
	var expired bool
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE ads SET status = 'expired', updated_at = now()
			WHERE id = $1 AND status = 'active' AND expires_at IS NOT NULL AND expires_at <= now()
		`, adID)
		if err != nil {
			return fmt.Errorf("repo: истечение объявления: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil // уже не активно или продлено — пропуск
		}
		expired = true
		return outbox.Enqueue(ctx, tx, ev)
	})
	return expired, err
}

// AddViews прибавляет накопленные просмотры к объявлениям одним batch-UPDATE
// (id → дельта). Несуществующие объявления просто не совпадают и отбрасываются.
// Устраняет write-hotspot: поток просмотров популярной карточки сливается пачкой.
func (r *Repo) AddViews(ctx context.Context, counts map[string]int64) error {
	if len(counts) == 0 {
		return nil
	}
	values := make([]string, 0, len(counts))
	args := make([]any, 0, len(counts)*2)
	i := 1
	for adID, delta := range counts {
		if delta == 0 {
			continue
		}
		values = append(values, fmt.Sprintf("($%d::uuid, $%d::bigint)", i, i+1))
		args = append(args, adID, delta)
		i += 2
	}
	if len(values) == 0 {
		return nil
	}
	sql := `UPDATE ads AS a SET views_count = a.views_count + v.delta
		FROM (VALUES ` + strings.Join(values, ",") + `) AS v(id, delta)
		WHERE a.id = v.id`
	if _, err := r.pool.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("repo: флеш просмотров: %w", err)
	}
	return nil
}

// UpdateWithEvent обновляет изменяемые поля объявления, заменяет значения
// атрибутов и изображения (replace-all) и кладёт событие в outbox — одной
// транзакцией. Статус передаётся уже вычисленным приложением (правка активного
// по ключевым полям → pending). ErrAdNotFound, если объявления нет.
func (r *Repo) UpdateWithEvent(ctx context.Context, a domain.Ad, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE ads SET category_id=$2, title=$3, description=$4, price=$5, currency=$6,
				region_id=$7, city_id=$8, lat=$9, lng=$10, status=$11, phone_display=$12, updated_at=now()
			WHERE id=$1
		`, a.ID, a.CategoryID, a.Title, a.Description, a.Price, a.Currency,
			a.RegionID, a.CityID, a.Lat, a.Lng, string(a.Status), a.PhoneDisplay)
		if err != nil {
			return fmt.Errorf("repo: обновление объявления: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrAdNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM ad_attribute_values WHERE ad_id=$1`, a.ID); err != nil {
			return fmt.Errorf("repo: очистка атрибутов: %w", err)
		}
		if err := insertAttributes(ctx, tx, a.ID, a.Attributes); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM ad_images WHERE ad_id=$1`, a.ID); err != nil {
			return fmt.Errorf("repo: очистка изображений: %w", err)
		}
		if err := insertImages(ctx, tx, a.ID, a.Images); err != nil {
			return err
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// DeleteWithEvent удаляет объявление (каскад на атрибуты/изображения) и
// публикует bozor.ad.deleted одной транзакцией. ErrAdNotFound, если записи нет.
func (r *Repo) DeleteWithEvent(ctx context.Context, adID string, ev events.Envelope) error {
	return pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM ads WHERE id=$1`, adID)
		if err != nil {
			return fmt.Errorf("repo: удаление объявления: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrAdNotFound
		}
		return outbox.Enqueue(ctx, tx, ev)
	})
}

// ListActive возвращает ленту активных объявлений (без дочерних сущностей —
// карточка ленты не требует полного набора атрибутов/изображений; детальный
// GET отдаёт всё). Фильтры по категории/региону опциональны, сортировка —
// по bumped_at/published_at (свежесть), либо по цене.
func (r *Repo) ListActive(ctx context.Context, f domain.FeedFilter) ([]domain.Ad, error) {
	conds := []string{"status = 'active'"}
	args := []any{}
	n := 1
	if f.CategoryID != "" {
		conds = append(conds, fmt.Sprintf("category_id = $%d::uuid", n))
		args = append(args, f.CategoryID)
		n++
	}
	if f.RegionID != 0 {
		conds = append(conds, fmt.Sprintf("region_id = $%d", n))
		args = append(args, f.RegionID)
		n++
	}
	order := "COALESCE(bumped_at, published_at) DESC NULLS LAST"
	switch f.Sort {
	case "price_asc":
		order = "price ASC"
	case "price_desc":
		order = "price DESC"
	}
	sql := `SELECT ` + adColumns + ` FROM ads WHERE ` + strings.Join(conds, " AND ") +
		` ORDER BY ` + order + fmt.Sprintf(", id DESC LIMIT $%d OFFSET $%d", n, n+1)
	args = append(args, f.Limit, f.Offset)
	return r.queryAds(ctx, sql, args...)
}

// ListByUser возвращает объявления пользователя (все или заданного статуса),
// новейшие сверху, без дочерних сущностей.
func (r *Repo) ListByUser(ctx context.Context, userID, status string, limit, offset int) ([]domain.Ad, error) {
	args := []any{userID}
	sql := `SELECT ` + adColumns + ` FROM ads WHERE user_id = $1::uuid`
	n := 2
	if status != "" {
		sql += fmt.Sprintf(" AND status = $%d", n)
		args = append(args, status)
		n++
	}
	sql += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", n, n+1)
	args = append(args, limit, offset)
	return r.queryAds(ctx, sql, args...)
}

// queryAds выполняет запрос списка объявлений (без дочерних сущностей).
func (r *Repo) queryAds(ctx context.Context, sql string, args ...any) ([]domain.Ad, error) {
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: запрос списка объявлений: %w", err)
	}
	defer rows.Close()

	var out []domain.Ad
	for rows.Next() {
		a, err := scanAd(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: чтение объявления списка: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: перебор списка объявлений: %w", err)
	}
	return out, nil
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
