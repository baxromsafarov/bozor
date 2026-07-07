//go:build integration

package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/auth/internal/domain"
	"bozor/services/auth/internal/repo"
)

func TestAuditRepo_Log(t *testing.T) {
	ctx := context.Background()
	pool := startAuthDB(t)
	a := repo.NewAuditRepo(pool)
	userID := insertUser(t, pool, 2001)

	// Запись с пользователем, валидным IP и деталями.
	require.NoError(t, a.Log(ctx, domain.AuditEntry{
		UserID: userID, Event: domain.AuditLogin, IP: "203.0.113.10",
		Detail: map[string]any{"device_id": "dev-1"},
	}))
	// Запись без пользователя и с невалидным IP (должны стать NULL).
	require.NoError(t, a.Log(ctx, domain.AuditEntry{Event: "misc", IP: "not-an-ip"}))

	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM auth_audit_log").Scan(&n))
	assert.Equal(t, 2, n)

	var (
		ip     *string
		uid    *string
		detail string
	)
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT host(ip), user_id::text, detail::text FROM auth_audit_log WHERE event=$1",
		domain.AuditLogin).Scan(&ip, &uid, &detail))
	require.NotNil(t, ip)
	assert.Equal(t, "203.0.113.10", *ip)
	require.NotNil(t, uid)
	assert.Equal(t, userID, *uid)
	assert.Contains(t, detail, "dev-1")

	var ip2 *string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT host(ip) FROM auth_audit_log WHERE event='misc'").Scan(&ip2))
	assert.Nil(t, ip2, "невалидный IP сохраняется как NULL")
}
