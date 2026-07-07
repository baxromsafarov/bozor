package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/authx"

	"bozor/services/auth/internal/domain"
)

// fakeRefreshStore фиксирует вставку/ротацию refresh-токенов.
type fakeRefreshStore struct {
	inserted  *domain.RefreshInsert
	rotateRes domain.RotationResult
	rotateErr error
	gotHash   []byte
	gotDevice string
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

const testKey = "test-signing-key"

func newTokenSvc(store RefreshStore) *TokenService {
	signer := authx.NewSigner([]byte(testKey), "auth", 15*time.Minute)
	return NewTokenService(signer, store, 720*time.Hour)
}

func TestIssueForUser_PersistsHashAndSignsAccess(t *testing.T) {
	store := &fakeRefreshStore{}
	pair, err := newTokenSvc(store).IssueForUser(context.Background(), "user-1", "dev-abc12345")
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
}

func TestIssueForUser_GeneratesDeviceIDWhenEmpty(t *testing.T) {
	store := &fakeRefreshStore{}
	pair, err := newTokenSvc(store).IssueForUser(context.Background(), "user-1", "")
	require.NoError(t, err)
	assert.NotEmpty(t, pair.DeviceID, "пустой device_id заменяется сгенерированным")
	require.NotNil(t, store.inserted)
	assert.Equal(t, pair.DeviceID, store.inserted.DeviceID)
}

func TestRefresh_RotatesAndSigns(t *testing.T) {
	store := &fakeRefreshStore{rotateRes: domain.RotationResult{UserID: "user-9", DeviceID: "dev-xyz98765"}}
	pair, err := newTokenSvc(store).Refresh(context.Background(), "old-refresh", "dev-xyz98765")
	require.NoError(t, err)

	assert.NotEmpty(t, pair.RefreshToken)
	assert.NotEqual(t, "old-refresh", pair.RefreshToken, "ротация выдаёт новый refresh-токен")
	assert.Equal(t, "user-9", pair.UserID)
	assert.Equal(t, domain.HashRefreshToken("old-refresh"), store.gotHash, "ротация ищет по хешу старого токена")
	assert.Equal(t, "dev-xyz98765", store.gotDevice)

	claims, err := authx.Parse([]byte(testKey), pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "user-9", claims.Subject)
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
			_, err := newTokenSvc(store).Refresh(context.Background(), "old", "dev-xyz98765")
			require.Error(t, err)
			assert.Equal(t, apperr.KindUnauthorized, apperr.KindOf(err),
				"ошибки ротации → 401, без утечки деталей")
		})
	}
}
