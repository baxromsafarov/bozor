package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"bozor/services/auth/internal/domain"
)

// AuditRepo пишет журнал чувствительных действий авторизации.
type AuditRepo struct {
	pool *pgxpool.Pool
}

// NewAuditRepo создаёт репозиторий аудита.
func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

// Log добавляет запись аудита. Пустые user_id/ip сохраняются как NULL;
// невалидный IP отбрасывается (не роняет запись).
func (r *AuditRepo) Log(ctx context.Context, e domain.AuditEntry) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("repo: генерация id аудита: %w", err)
	}

	detail := []byte("{}")
	if e.Detail != nil {
		if b, err := json.Marshal(e.Detail); err == nil {
			detail = b
		}
	}

	var userID *string
	if e.UserID != "" {
		userID = &e.UserID
	}
	var ip *string
	if e.IP != "" && net.ParseIP(e.IP) != nil {
		ip = &e.IP
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO auth_audit_log (id, user_id, event, detail, ip)
		VALUES ($1, $2, $3, $4, $5::inet)
	`, id.String(), userID, e.Event, detail, ip)
	if err != nil {
		return fmt.Errorf("repo: запись аудита: %w", err)
	}
	return nil
}
