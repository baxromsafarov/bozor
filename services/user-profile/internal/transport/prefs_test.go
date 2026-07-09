package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/user-profile/internal/domain"
)

func TestHandler_GetPrefs(t *testing.T) {
	fs := &fakeService{prefs: domain.DefaultNotificationPrefs()}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	withUser(h.GetPrefs, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me/notification-prefs", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body prefsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Len(t, body.Prefs, 5)
	assert.Equal(t, "u1", fs.gotOwner)
}

func TestHandler_GetPrefs_Unauthorized(t *testing.T) {
	h := NewHandler(&fakeService{}, discardLogger())
	rec := httptest.NewRecorder()
	withUser(h.GetPrefs, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me/notification-prefs", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_PutPrefs(t *testing.T) {
	fs := &fakeService{prefs: domain.DefaultNotificationPrefs()}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"prefs":[{"channel":"telegram","event_type":"ad_status","enabled":false}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/me/notification-prefs", body)
	withUser(h.PutPrefs, "u1").ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, fs.gotPrefs, 1)
	assert.Equal(t, "ad_status", fs.gotPrefs[0].EventType)
	assert.False(t, fs.gotPrefs[0].Enabled)
}

func TestHandler_PutPrefs_Invalid(t *testing.T) {
	fs := &fakeService{err: domain.ErrInvalidNotificationPref}
	h := NewHandler(fs, discardLogger())
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"prefs":[{"channel":"email","event_type":"ad_status","enabled":true}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/me/notification-prefs", body)
	withUser(h.PutPrefs, "u1").ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_notification_pref")
}
