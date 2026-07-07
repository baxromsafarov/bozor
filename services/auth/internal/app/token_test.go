package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"

	"bozor/services/auth/internal/domain"
)

// fakeRefreshStore фиксирует вставку/ротацию/отзыв refresh-токенов.
type fakeRefreshStore struct {
	inserted    *domain.RefreshInsert
	rotateRes   domain.RotationResult
	rotateErr   error
	gotHash     []byte
	gotDevice   string
	revokeUser  string
	revokeFound bool
	revokeHash  []byte
}

func (f *fakeRefreshStore) Insert(_ context.Context, in domain.RefreshInsert) error {
	f.inserted = &in
	return nil
}

func (f *fakeRefreshStore) Rotate(_ context.Context, oldHash []byte, expectedDevice, _ string, _ []byte, _ time.Time) (domain.RotationResult, error) {
	f.gotHash = oldHash
	f.gotDevice = expectedDevice
	return f.rotateRes, f.rotateErr
}

func (f *fakeRefreshStore) RevokeFamily(_ context.Context, hash []byte) (string, bool, error) {
	f.revokeHash = hash
	return f.revokeUser, f.revokeFound, nil
}

// fakeAuditor собирает записи аудита.
type fakeAuditor struct {
	entries []domain.AuditEntry
}

func (a *fakeAuditor) Log(_ context.Context, e domain.AuditEntry) error {
	a.entries = append(a.entries, e)
	return nil
}

const testKey = "test-signing-key"

func newTokenSvc(store RefreshStore) *TokenService {
	return newTokenSvcAudit(store, nil)
}

func newTokenSvcAudit(store RefreshStore, auditor Auditor) *TokenService {
	signer := authx.NewSigner([]byte(testKey), "auth", 15*time.Minute)
	return NewTokenService(signer, store, auditor, 720*time.Hour, discardLogger())
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestIssueForUser_PersistsHashAndSignsAccess(t *testing.T) {
	store := &fakeRefreshStore{}
	auditor := &fakeAuditor{}
	pair, err := newTokenSvcAudit(store, auditor).IssueForUser(context.Background(), "user-1", "dev-abc12345", "203.0.113.7")
	require.NoError(t, err)

	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, domain.TokenTypeBearer, pair.TokenType)
	assert.Positive(t, pair.ExpiresIn)
	assert.Equal(t, "dev-abc12345", pair.DeviceID)

	require.NotNil(t, store.inserted)
	assert.Equal(t, "user-1", store.inserted.UserID)
	assert.NotEmpty(t, store.inserted.FamilyID)
	assert.Equal(t, domain.HashRefreshToken(pair.RefreshToken), store.inserted.TokenHash,
		"в БД хранится хеш, а не сам refresh-токен")

	claims, err := authx.Parse([]byte(testKey), pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.Subject)
	assert.Contains(t, claims.Roles, domain.RoleUser)
	assert.NotEmpty(t, claims.ID, "jti проставлен")

	require.Len(t, auditor.entries, 1, "вход пишется в аудит")
	assert.Equal(t, domain.AuditLogin, auditor.entries[0].Event)
	assert.Equal(t, "user-1", auditor.entries[0].UserID)
	assert.Equal(t, "203.0.113.7", auditor.entries[0].IP)
}

func TestIssueForUser_GeneratesDeviceIDWhenEmpty(t *testing.T) {
	store := &fakeRefreshStore{}
	pair, err := newTokenSvc(store).IssueForUser(context.Background(), "user-1", "", "")
	require.NoError(t, err)
	assert.NotEmpty(t, pair.DeviceID, "пустой device_id заменяется сгенерированным")
	require.NotNil(t, store.inserted)
	assert.Equal(t, pair.DeviceID, store.inserted.DeviceID)
}

func TestRefresh_RotatesAndSigns(t *testing.T) {
	store := &fakeRefreshStore{rotateRes: domain.RotationResult{UserID: "user-9", DeviceID: "dev-xyz98765"}}
	auditor := &fakeAuditor{}
	pair, err := newTokenSvcAudit(store, auditor).Refresh(context.Background(), "old-refresh", "dev-xyz98765", "198.51.100.5")
	require.NoError(t, err)

	assert.NotEmpty(t, pair.RefreshToken)
	assert.NotEqual(t, "old-refresh", pair.RefreshToken, "ротация выдаёт новый refresh-токен")
	assert.Equal(t, "user-9", pair.UserID)
	assert.Equal(t, domain.HashRefreshToken("old-refresh"), store.gotHash, "ротация ищет по хешу старого токена")
	assert.Equal(t, "dev-xyz98765", store.gotDevice)

	claims, err := authx.Parse([]byte(testKey), pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "user-9", claims.Subject)

	require.Len(t, auditor.entries, 1)
	assert.Equal(t, domain.AuditTokenRefreshed, auditor.entries[0].Event)
}

func TestRefresh_ErrorMapping(t *testing.T) {
	cases := map[string]error{
		"reuse":     domain.ErrTokenReuse,
		"not_found": domain.ErrTokenNotFound,
		"expired":   domain.ErrTokenExpired,
		"device":    domain.ErrDeviceMismatch,
	}
	for name, sentinel := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeRefreshStore{rotateErr: sentinel}
			_, err := newTokenSvc(store).Refresh(context.Background(), "old", "dev-xyz98765", "")
			require.Error(t, err)
			assert.Equal(t, apperr.KindUnauthorized, apperr.KindOf(err),
				"ошибки ротации → 401, без утечки деталей")
		})
	}
}

func TestLogout_RevokesFamilyAndAudits(t *testing.T) {
	store := &fakeRefreshStore{revokeUser: "user-7", revokeFound: true}
	auditor := &fakeAuditor{}
	err := newTokenSvcAudit(store, auditor).Logout(context.Background(), "some-refresh", "203.0.113.9")
	require.NoError(t, err)
	assert.Equal(t, domain.HashRefreshToken("some-refresh"), store.revokeHash)
	require.Len(t, auditor.entries, 1)
	assert.Equal(t, domain.AuditLogout, auditor.entries[0].Event)
	assert.Equal(t, "user-7", auditor.entries[0].UserID)
}

func TestLogout_UnknownTokenNoAudit(t *testing.T) {
	store := &fakeRefreshStore{revokeFound: false}
	auditor := &fakeAuditor{}
	err := newTokenSvcAudit(store, auditor).Logout(context.Background(), "unknown", "")
	require.NoError(t, err, "неизвестный токен не ошибка (идемпотентность)")
	assert.Empty(t, auditor.entries, "нет пользователя — нет записи аудита")
}
