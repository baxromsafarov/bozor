package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/authx"

	"bozor/services/user-profile/internal/app"
	"bozor/services/user-profile/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func timeAny() time.Time { return time.Unix(1700000000, 0).UTC() }

// fakeService — управляемая реализация transport.Service.
type fakeService struct {
	profile  domain.Profile
	public   app.PublicProfile
	prefs    []domain.NotificationPref
	err      error
	gotOwner string
	gotIn    app.UpdateInput
	gotPrefs []domain.NotificationPref
}

func (f *fakeService) Me(_ context.Context, userID string) (domain.Profile, error) {
	f.gotOwner = userID
	return f.profile, f.err
}

func (f *fakeService) UpdateMe(_ context.Context, userID string, in app.UpdateInput) (domain.Profile, error) {
	f.gotOwner, f.gotIn = userID, in
	return f.profile, f.err
}

func (f *fakeService) PublicProfile(_ context.Context, userID string) (app.PublicProfile, error) {
	f.gotOwner = userID
	return f.public, f.err
}

func (f *fakeService) NotificationPrefs(_ context.Context, userID string) ([]domain.NotificationPref, error) {
	f.gotOwner = userID
	return f.prefs, f.err
}

func (f *fakeService) SetNotificationPrefs(_ context.Context, userID string, prefs []domain.NotificationPref) ([]domain.NotificationPref, error) {
	f.gotOwner, f.gotPrefs = userID, prefs
	return f.prefs, f.err
}

// withUser выполняет запрос через middleware проброшенной идентичности.
func withUser(h http.HandlerFunc, userID string) http.Handler {
	handler := authx.FromForwardedHeaders(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userID != "" {
			r.Header.Set("X-User-Id", userID)
		}
		handler.ServeHTTP(w, r)
	})
}

func TestHandler_Me_Unauthorized(t *testing.T) {
	h := NewHandler(&fakeService{}, discardLogger())
	rec := httptest.NewRecorder()
	withUser(h.Me, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_Me_OK(t *testing.T) {
	fs := &fakeService{profile: domain.NewDefaultProfile("u1", "ru", timeAny())}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	withUser(h.Me, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body profileResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "u1", body.UserID)
	assert.Equal(t, "individual", body.UserType)
	assert.Equal(t, "u1", fs.gotOwner)
}

func TestHandler_UpdateMe_ParsesBody(t *testing.T) {
	fs := &fakeService{profile: domain.NewDefaultProfile("u1", "ru", timeAny())}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"display_name":"Азиз","city_id":14}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/me", body)
	withUser(h.UpdateMe, "u1").ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, fs.gotIn.DisplayName)
	assert.Equal(t, "Азиз", *fs.gotIn.DisplayName)
	require.NotNil(t, fs.gotIn.CityID)
	assert.Equal(t, int64(14), *fs.gotIn.CityID)
}

func TestHandler_UpdateMe_ValidationError(t *testing.T) {
	fs := &fakeService{err: domain.ErrInvalidUserType}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/me", strings.NewReader(`{"user_type":"x"}`))
	withUser(h.UpdateMe, "u1").ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_user_type")
}

func TestHandler_PublicProfile(t *testing.T) {
	fs := &fakeService{public: app.PublicProfile{
		Profile: domain.NewDefaultProfile("seller", "ru", timeAny()),
		Rating:  domain.Rating{AvgRating: 4.5, ReviewsCount: 12},
	}}
	h := NewHandler(fs, discardLogger())

	// Через chi-роутер, чтобы разобрать URL-параметр {id}.
	r := chi.NewRouter()
	r.Get("/api/v1/users/{id}", h.PublicProfile)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/users/seller", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body publicProfileResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "seller", body.UserID)
	assert.Equal(t, 4.5, body.Rating.AvgRating)
	assert.Equal(t, "seller", fs.gotOwner)
}

func TestHandler_PublicProfile_NotFound(t *testing.T) {
	fs := &fakeService{err: domain.ErrProfileNotFound}
	h := NewHandler(fs, discardLogger())
	r := chi.NewRouter()
	r.Get("/api/v1/users/{id}", h.PublicProfile)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/users/ghost", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "profile_not_found")
}
