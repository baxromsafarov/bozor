package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/authx"

	"bozor/services/moderation/internal/app"
	"bozor/services/moderation/internal/domain"
)

type fakeDecider struct {
	err error

	action, gotAdID, gotModerator, gotReason string
}

func (f *fakeDecider) Approve(_ context.Context, adID, moderatorID string) error {
	f.action, f.gotAdID, f.gotModerator = "approve", adID, moderatorID
	return f.err
}
func (f *fakeDecider) Reject(_ context.Context, adID, moderatorID, reason string) error {
	f.action, f.gotAdID, f.gotModerator, f.gotReason = "reject", adID, moderatorID, reason
	return f.err
}
func (f *fakeDecider) RequestEdit(_ context.Context, adID, moderatorID, reason string) error {
	f.action, f.gotAdID, f.gotModerator, f.gotReason = "request-edit", adID, moderatorID, reason
	return f.err
}

func decisionRouter(h *DecisionHandler, userID, roles string) http.Handler {
	r := chi.NewRouter()
	r.Use(authx.FromForwardedHeaders)
	r.Use(requireStaff)
	r.Post("/api/v1/moderation/tasks/{adId}/approve", h.Approve)
	r.Post("/api/v1/moderation/tasks/{adId}/reject", h.Reject)
	r.Post("/api/v1/moderation/tasks/{adId}/request-edit", h.RequestEdit)
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

func post(t *testing.T, router http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	router.ServeHTTP(rec, req)
	return rec
}

func TestApprove_OK(t *testing.T) {
	fd := &fakeDecider{}
	rec := post(t, decisionRouter(NewDecisionHandler(fd, discardLog()), "mod1", "moderator"),
		"/api/v1/moderation/tasks/ad1/approve", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "approve", fd.action)
	assert.Equal(t, "ad1", fd.gotAdID)
	assert.Equal(t, "mod1", fd.gotModerator)
}

func TestReject_OK(t *testing.T) {
	fd := &fakeDecider{}
	rec := post(t, decisionRouter(NewDecisionHandler(fd, discardLog()), "mod1", "admin"),
		"/api/v1/moderation/tasks/ad1/reject", `{"reason":"копия"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "reject", fd.action)
	assert.Equal(t, "копия", fd.gotReason)
}

func TestRequestEdit_OK(t *testing.T) {
	fd := &fakeDecider{}
	rec := post(t, decisionRouter(NewDecisionHandler(fd, discardLog()), "mod1", "moderator"),
		"/api/v1/moderation/tasks/ad1/request-edit", `{"reason":"уточните"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "request-edit", fd.action)
}

func TestDecision_Forbidden(t *testing.T) {
	fd := &fakeDecider{}
	rec := post(t, decisionRouter(NewDecisionHandler(fd, discardLog()), "u1", "user"),
		"/api/v1/moderation/tasks/ad1/approve", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, fd.action)
}

func TestDecision_Anonymous(t *testing.T) {
	fd := &fakeDecider{}
	rec := post(t, decisionRouter(NewDecisionHandler(fd, discardLog()), "", ""),
		"/api/v1/moderation/tasks/ad1/approve", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDecision_NotFound_404(t *testing.T) {
	fd := &fakeDecider{err: app.ErrTaskNotFound}
	rec := post(t, decisionRouter(NewDecisionHandler(fd, discardLog()), "mod1", "moderator"),
		"/api/v1/moderation/tasks/ad1/approve", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDecision_AlreadyDecided_409(t *testing.T) {
	fd := &fakeDecider{err: app.ErrNotPending}
	rec := post(t, decisionRouter(NewDecisionHandler(fd, discardLog()), "mod1", "moderator"),
		"/api/v1/moderation/tasks/ad1/approve", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestReject_ReasonRequired_422(t *testing.T) {
	fd := &fakeDecider{err: domain.ErrReasonRequired}
	rec := post(t, decisionRouter(NewDecisionHandler(fd, discardLog()), "mod1", "moderator"),
		"/api/v1/moderation/tasks/ad1/reject", `{"reason":""}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}
