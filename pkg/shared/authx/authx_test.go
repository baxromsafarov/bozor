package authx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/apperr"
)

var testKey = []byte("test-secret-key")

// TestSignParseRoundTrip проверяет выпуск токена и его обратный разбор.
func TestSignParseRoundTrip(t *testing.T) {
	signer := NewSigner(testKey, "bozor-auth", 15*time.Minute)

	before := time.Now()
	token, jti, expiresAt, err := signer.Sign("user-42", []string{"buyer", "seller"})
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// jti — валидный UUID версии 7.
	id, err := uuid.Parse(jti)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), id.Version())

	// expiresAt — примерно now+TTL.
	assert.WithinDuration(t, before.Add(15*time.Minute), expiresAt, 5*time.Second)

	claims, err := Parse(testKey, token)
	require.NoError(t, err)
	assert.Equal(t, "user-42", claims.Subject)
	assert.Equal(t, []string{"buyer", "seller"}, claims.Roles)
	assert.Equal(t, jti, claims.ID)
	assert.Equal(t, "bozor-auth", claims.Issuer)
	require.NotNil(t, claims.ExpiresAt)
	assert.WithinDuration(t, expiresAt, claims.ExpiresAt.Time, time.Second)
	require.NotNil(t, claims.IssuedAt)
}

// TestParseErrors проверяет, что любые невалидные токены дают KindUnauthorized.
func TestParseErrors(t *testing.T) {
	signer := NewSigner(testKey, "bozor-auth", 15*time.Minute)
	validToken, _, _, err := signer.Sign("user-1", nil)
	require.NoError(t, err)

	// Просроченный токен: истёк 2 минуты назад (leeway 30 c не спасает).
	expiredSigner := NewSigner(testKey, "bozor-auth", -2*time.Minute)
	expiredToken, _, _, err := expiredSigner.Sign("user-1", nil)
	require.NoError(t, err)

	// Токен с alg=none.
	noneToken, err := jwt.NewWithClaims(jwt.SigningMethodNone, &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	tests := []struct {
		name  string
		key   []byte
		token string
	}{
		{name: "чужой ключ", key: []byte("another-key"), token: validToken},
		{name: "просроченный токен", key: testKey, token: expiredToken},
		{name: "alg=none", key: testKey, token: noneToken},
		{name: "мусор вместо токена", key: testKey, token: "not.a.jwt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := Parse(tc.key, tc.token)
			require.Error(t, err)
			assert.Nil(t, claims)
			assert.Equal(t, apperr.KindUnauthorized, apperr.KindOf(err))

			var appErr *apperr.Error
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, "invalid_token", appErr.Code)
		})
	}
}

// ctxEcho — хендлер, отдающий данные аутентификации из контекста.
func ctxEcho(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(map[string]any{
			"user_id": UserID(ctx),
			"roles":   Roles(ctx),
			"jti":     JTI(ctx),
		})
		require.NoError(t, err)
	})
}

// TestAuthMiddleware проверяет middleware Auth: 401 без токена,
// 401 при невалидном токене и заполнение контекста при валидном.
func TestAuthMiddleware(t *testing.T) {
	signer := NewSigner(testKey, "bozor-auth", 15*time.Minute)
	token, jti, _, err := signer.Sign("user-7", []string{"admin"})
	require.NoError(t, err)

	handler := Auth(testKey)(ctxEcho(t))

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
		wantCode   string // код apperr в поле type проблемы
	}{
		{name: "без заголовка", authHeader: "", wantStatus: http.StatusUnauthorized, wantCode: "missing_token"},
		{name: "не Bearer", authHeader: "Basic abc", wantStatus: http.StatusUnauthorized, wantCode: "missing_token"},
		{name: "невалидный токен", authHeader: "Bearer garbage", wantStatus: http.StatusUnauthorized, wantCode: "invalid_token"},
		{name: "валидный токен", authHeader: "Bearer " + token, wantStatus: http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code)
			if tc.wantStatus != http.StatusOK {
				assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
				var p apperr.Problem
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
				assert.Equal(t, tc.wantStatus, p.Status)
				assert.Equal(t, "https://bozor.uz/problems/"+tc.wantCode, p.Type)
				assert.NotEmpty(t, p.Detail)
				return
			}

			var got struct {
				UserID string   `json:"user_id"`
				Roles  []string `json:"roles"`
				JTI    string   `json:"jti"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.Equal(t, "user-7", got.UserID)
			assert.Equal(t, []string{"admin"}, got.Roles)
			assert.Equal(t, jti, got.JTI)
		})
	}
}

// TestRequireRole проверяет middleware RequireRole: 403 без роли, 200 с ролью.
func TestRequireRole(t *testing.T) {
	tests := []struct {
		name       string
		ctxRoles   []string
		required   string
		wantStatus int
	}{
		{name: "роль есть", ctxRoles: []string{"buyer", "admin"}, required: "admin", wantStatus: http.StatusOK},
		{name: "роли нет", ctxRoles: []string{"buyer"}, required: "admin", wantStatus: http.StatusForbidden},
		{name: "контекст пуст", ctxRoles: nil, required: "admin", wantStatus: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := RequireRole(tc.required)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
			if tc.ctxRoles != nil {
				req = req.WithContext(withAuth(req.Context(), "user-1", tc.ctxRoles, "jti-1"))
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code)
			if tc.wantStatus == http.StatusForbidden {
				assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
				var p apperr.Problem
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
				assert.Equal(t, http.StatusForbidden, p.Status)
				assert.Equal(t, "https://bozor.uz/problems/forbidden", p.Type)
			}
		})
	}
}

// TestFromForwardedHeaders проверяет чтение X-User-Id и X-User-Roles.
func TestFromForwardedHeaders(t *testing.T) {
	tests := []struct {
		name      string
		userID    string
		rolesHdr  string
		wantID    string
		wantRoles []string
	}{
		{
			name:      "id и роли",
			userID:    "user-9",
			rolesHdr:  "buyer, seller ,admin",
			wantID:    "user-9",
			wantRoles: []string{"buyer", "seller", "admin"},
		},
		{
			name:   "только id",
			userID: "user-9",
			wantID: "user-9",
		},
		{
			name: "без заголовков",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotID, gotJTI string
			var gotRoles []string
			handler := FromForwardedHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotID = UserID(r.Context())
				gotRoles = Roles(r.Context())
				gotJTI = JTI(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/internal/orders", nil)
			if tc.userID != "" {
				req.Header.Set("X-User-Id", tc.userID)
			}
			if tc.rolesHdr != "" {
				req.Header.Set("X-User-Roles", tc.rolesHdr)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantID, gotID)
			assert.Equal(t, tc.wantRoles, gotRoles)
			assert.Empty(t, gotJTI, "jti не должен появляться из заголовков gateway")
		})
	}
}

// TestHasRole проверяет HasRole на разных контекстах.
func TestHasRole(t *testing.T) {
	ctx := withAuth(t.Context(), "u", []string{"buyer"}, "")
	assert.True(t, HasRole(ctx, "buyer"))
	assert.False(t, HasRole(ctx, "admin"))
	assert.False(t, HasRole(t.Context(), "buyer"))
}
