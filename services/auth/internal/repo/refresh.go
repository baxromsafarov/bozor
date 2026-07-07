package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/pkg/shared/pgxx"

	"bozor/services/auth/internal/domain"
)

// RefreshRepo — репозиторий refresh-токенов (ротация, reuse-detection).
type RefreshRepo struct {
	pool *pgxpool.Pool
}

// NewRefreshRepo создаёт репозиторий поверх пула соединений.
func NewRefreshRepo(pool *pgxpool.Pool) *RefreshRepo {
	return &RefreshRepo{pool: pool}
}

// Insert сохраняет новый refresh-токен (только хеш) — при первичном входе.
func (r *RefreshRepo) Insert(ctx context.Context, in domain.RefreshInsert) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (id, user_id, token_hash, device_id, family_id, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, in.ID, in.UserID, in.TokenHash, in.DeviceID, in.FamilyID, in.ExpiresAt)
	if err != nil {
		return fmt.Errorf("repo: вставка refresh-токена: %w", err)
	}
	return nil
}

// Rotate атомарно ротирует refresh-токен по хешу oldHash:
//   - активный не истёкший токen с совпадающим устройством помечается revoked,
//     а его место занимает новый токен (newIn) в том же семействе;
//   - повторное использование (токен найден, но уже revoked) трактуется как
//     кража: отзывается всё семейство и пишется аудит-событие (ErrTokenReuse);
//   - отсутствие токена — ErrTokenNotFound, истечение — ErrTokenExpired,
//     чужое устройство — ErrDeviceMismatch.
//
// Возвращает данные исходного токена для выпуска нового access-токена.
func (r *RefreshRepo) Rotate(ctx context.Context, oldHash []byte, expectedDevice, newID string, newHash []byte, newExpiresAt time.Time) (domain.RotationResult, error) {
	var (
		res domain.RotationResult
		// outcome — доменный исход, возвращаемый ПОСЛЕ коммита. Внутри tx его
		// нельзя вернуть как ошибку: это откатило бы побочные эффекты
		// (отзыв семейства при reuse, гашение истёкшего токена).
		outcome error
	)
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var (
			id, userID, familyID, deviceID string
			revokedAt                      *time.Time
			expiresAt                      time.Time
		)
		e := tx.QueryRow(ctx, `
			SELECT id, user_id, family_id, device_id, revoked_at, expires_at
			FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE
		`, oldHash).Scan(&id, &userID, &familyID, &deviceID, &revokedAt, &expiresAt)
		if errors.Is(e, pgx.ErrNoRows) {
			outcome = domain.ErrTokenNotFound
			return nil
		}
		if e != nil {
			return fmt.Errorf("repo: чтение refresh-токена: %w", e)
		}

		// Reuse-detection: предъявлен уже погашенный токен — отзыв семейства
		// и аудит должны быть закоммичены, поэтому tx завершается без ошибки.
		if revokedAt != nil {
			if _, e := tx.Exec(ctx, `
				UPDATE refresh_tokens SET revoked_at = now()
				WHERE family_id = $1 AND revoked_at IS NULL
			`, familyID); e != nil {
				return fmt.Errorf("repo: отзыв семейства: %w", e)
			}
			if e := auditReuse(ctx, tx, userID, familyID); e != nil {
				return e
			}
			outcome = domain.ErrTokenReuse
			return nil
		}
		if !expiresAt.After(time.Now()) {
			if _, e := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1`, id); e != nil {
				return fmt.Errorf("repo: гашение истёкшего токена: %w", e)
			}
			outcome = domain.ErrTokenExpired
			return nil
		}
		if deviceID != expectedDevice {
			outcome = domain.ErrDeviceMismatch
			return nil
		}

		// Ротация: гасим текущий токен и вставляем новый в том же семействе.
		if _, e := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1`, id); e != nil {
			return fmt.Errorf("repo: гашение refresh-токена: %w", e)
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO refresh_tokens (id, user_id, token_hash, device_id, family_id, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, newID, userID, newHash, deviceID, familyID, newExpiresAt); e != nil {
			return fmt.Errorf("repo: вставка ротированного токена: %w", e)
		}

		res = domain.RotationResult{UserID: userID, FamilyID: familyID, DeviceID: deviceID}
		return nil
	})
	if err != nil {
		return domain.RotationResult{}, err
	}
	if outcome != nil {
		return domain.RotationResult{}, outcome
	}
	return res, nil
}

// auditReuse записывает событие обнаружения повторного использования токена.
func auditReuse(ctx context.Context, tx pgx.Tx, userID, familyID string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("repo: генерация id аудита: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_audit_log (id, user_id, event, detail)
		VALUES ($1, $2, 'refresh_reuse_detected', jsonb_build_object('family_id', $3::text))
	`, id.String(), userID, familyID)
	if err != nil {
		return fmt.Errorf("repo: запись аудита reuse: %w", err)
	}
	return nil
}
