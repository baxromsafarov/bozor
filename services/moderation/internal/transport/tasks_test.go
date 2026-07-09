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

	"bozor/services/moderation/internal/domain"
)

type fakeReader struct {
	tasks     []domain.Task
	gotStatus string
	gotLimit  int
}

func (f *fakeReader) ListTasks(_ context.Context, status string, limit int) ([]domain.Task, error) {
	f.gotStatus = status
	f.gotLimit = limit
	return f.tasks, nil
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// routerWith собирает мини-роутер с идентичностью/ролями из заголовков.
func routerWith(h *Handler, userID, roles string) http.Handler {
	r := chi.NewRouter()
	r.Use(authx.FromForwardedHeaders)
	r.Use(requireStaff)
	r.Get("/api/v1/moderation/tasks", h.ListTasks)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if userID != "" {
			req.Header.Set("X-User-Id", userID)
		}
		if roles != "" {
			req.Header.Set("X-User-Roles", roles)
		}
		r.ServeHTTP(w, req)
	})
}

func TestListTasks_Anonymous_401(t *testing.T) {
	h := NewHandler(&fakeReader{}, discardLog())
	rec := httptest.NewRecorder()
	routerWith(h, "", "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/moderation/tasks", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestListTasks_NonStaff_403(t *testing.T) {
	h := NewHandler(&fakeReader{}, discardLog())
	rec := httptest.NewRecorder()
	routerWith(h, "u1", "user").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/moderation/tasks", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestListTasks_Moderator_ReturnsQueue(t *testing.T) {
	fr := &fakeReader{tasks: []domain.Task{
		{ID: "t1", AdID: "ad1", UserID: "u1", Title: "Копия iPhone", Status: domain.StatusManual,
			AutoResult: domain.AutoFlagged, Reasons: []string{"stopword:копия"},
			CreatedAt: time.Unix(1700000000, 0).UTC(), UpdatedAt: time.Unix(1700000000, 0).UTC()},
	}}
	h := NewHandler(fr, discardLog())

	rec := httptest.NewRecorder()
	routerWith(h, "mod1", "moderator").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/moderation/tasks", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, domain.StatusManual, fr.gotStatus) // по умолчанию — ручная очередь
	assert.Equal(t, defaultLimit, fr.gotLimit)

	var body listResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Tasks, 1)
	assert.Equal(t, "ad1", body.Tasks[0].AdID)
	assert.Equal(t, []string{"stopword:копия"}, body.Tasks[0].Reasons)
}

func TestListTasks_InvalidStatus_422(t *testing.T) {
	h := NewHandler(&fakeReader{}, discardLog())
	rec := httptest.NewRecorder()
	routerWith(h, "mod1", "admin").ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/v1/moderation/tasks?status=weird", nil))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestListTasks_ClampsLimit(t *testing.T) {
	fr := &fakeReader{}
	h := NewHandler(fr, discardLog())
	rec := httptest.NewRecorder()
	routerWith(h, "mod1", "admin").ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/v1/moderation/tasks?limit=9999", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, maxLimit, fr.gotLimit)
}
