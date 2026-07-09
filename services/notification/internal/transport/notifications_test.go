package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/authx"

	"bozor/services/notification/internal/domain"
)

type fakeReader struct {
	items    []domain.Notification
	gotUser  string
	gotLimit int
	err      error
}

func (f *fakeReader) ListByUser(_ context.Context, userID string, limit int) ([]domain.Notification, error) {
	f.gotUser = userID
	f.gotLimit = limit
	return f.items, f.err
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func routerWith(h *Handler, userID string) http.Handler {
	r := chi.NewRouter()
	r.Use(authx.FromForwardedHeaders)
	r.Get("/api/v1/notifications", h.List)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if userID != "" {
			req.Header.Set("X-User-Id", userID)
		}
		r.ServeHTTP(w, req)
	})
}

func TestList_Unauthorized(t *testing.T) {
	h := NewHandler(&fakeReader{}, discardLog())
	rec := httptest.NewRecorder()
	routerWith(h, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestList_ReturnsHistory(t *testing.T) {
	sent := time.Unix(1700000100, 0).UTC()
	fr := &fakeReader{items: []domain.Notification{
		{ID: "n1", EventType: "bozor.ad.approved", Channel: "telegram", Body: "одобрено",
			Status: domain.StatusSent, Attempts: 1, CreatedAt: time.Unix(1700000000, 0).UTC(), SentAt: &sent},
		{ID: "n2", EventType: "bozor.saved_search.matched", Channel: "telegram", Body: "совпадение",
			Status: domain.StatusSkipped, Reason: domain.ReasonPrefsDisabled, CreatedAt: time.Unix(1700000050, 0).UTC()},
	}}
	h := NewHandler(fr, discardLog())

	rec := httptest.NewRecorder()
	routerWith(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "u1", fr.gotUser)
	assert.Equal(t, defaultLimit, fr.gotLimit)

	var body listResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Notifications, 2)
	assert.Equal(t, domain.StatusSent, body.Notifications[0].Status)
	require.NotNil(t, body.Notifications[0].SentAt)
	assert.Equal(t, domain.ReasonPrefsDisabled, body.Notifications[1].Reason)
	assert.Nil(t, body.Notifications[1].SentAt)
}

func TestList_ClampsLimit(t *testing.T) {
	fr := &fakeReader{}
	h := NewHandler(fr, discardLog())

	rec := httptest.NewRecorder()
	routerWith(h, "u1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/notifications?limit=9999", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, maxLimit, fr.gotLimit)
}
