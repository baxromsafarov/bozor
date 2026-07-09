package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/authx"

	"bozor/services/moderation/internal/app"
	"bozor/services/moderation/internal/domain"
)

type fakeOps struct {
	createErr, resolveErr, banErr error

	gotReporter, gotAction, gotBanUser string
	gotDuration                        time.Duration
}

func (f *fakeOps) CreateReport(_ context.Context, reporterID, targetType, targetID, reason string) (domain.Report, error) {
	f.gotReporter = reporterID
	if f.createErr != nil {
		return domain.Report{}, f.createErr
	}
	return domain.Report{ID: "r1", ReporterID: reporterID, TargetType: targetType, TargetID: targetID,
		Reason: reason, Status: domain.ReportOpen, CreatedAt: time.Unix(1700000000, 0).UTC()}, nil
}
func (f *fakeOps) ListReports(context.Context, string, int) ([]domain.Report, error) {
	return []domain.Report{{ID: "r1", TargetType: domain.TargetAd, Status: domain.ReportOpen, CreatedAt: time.Unix(1, 0).UTC()}}, nil
}
func (f *fakeOps) ResolveReport(_ context.Context, _, _, action, _ string) error {
	f.gotAction = action
	return f.resolveErr
}
func (f *fakeOps) BanUser(_ context.Context, _, userID, banType, _ string, d time.Duration) (domain.Ban, error) {
	f.gotBanUser, f.gotDuration = userID, d
	if f.banErr != nil {
		return domain.Ban{}, f.banErr
	}
	return domain.Ban{ID: "b1", UserID: userID, Type: banType}, nil
}

func reportRouter(h *ReportHandler, userID, roles string) http.Handler {
	r := chi.NewRouter()
	r.Group(func(pub chi.Router) {
		pub.Use(authx.FromForwardedHeaders)
		pub.Post("/api/v1/reports", h.Create)
	})
	r.Group(func(st chi.Router) {
		st.Use(authx.FromForwardedHeaders)
		st.Use(requireStaff)
		st.Get("/api/v1/moderation/reports", h.List)
		st.Post("/api/v1/moderation/reports/{id}/resolve", h.Resolve)
		st.Post("/api/v1/moderation/bans", h.Ban)
	})
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

func TestCreateReport_201(t *testing.T) {
	fo := &fakeOps{}
	rec := post(t, reportRouter(NewReportHandler(fo, discardLog()), "u1", ""),
		"/api/v1/reports", `{"target_type":"ad","target_id":"ad1","reason":"спам"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "u1", fo.gotReporter)
}

func TestCreateReport_Anonymous_401(t *testing.T) {
	fo := &fakeOps{}
	rec := post(t, reportRouter(NewReportHandler(fo, discardLog()), "", ""),
		"/api/v1/reports", `{"target_type":"ad","target_id":"ad1","reason":"спам"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateReport_InvalidTarget_422(t *testing.T) {
	fo := &fakeOps{createErr: domain.ErrInvalidTarget}
	rec := post(t, reportRouter(NewReportHandler(fo, discardLog()), "u1", ""),
		"/api/v1/reports", `{"target_type":"planet","target_id":"x","reason":"спам"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestListReports_StaffOnly(t *testing.T) {
	fo := &fakeOps{}
	rec := httptest.NewRecorder()
	reportRouter(NewReportHandler(fo, discardLog()), "u1", "user").
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/moderation/reports", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec = httptest.NewRecorder()
	reportRouter(NewReportHandler(fo, discardLog()), "mod1", "moderator").
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/moderation/reports", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestResolveReport_200(t *testing.T) {
	fo := &fakeOps{}
	rec := post(t, reportRouter(NewReportHandler(fo, discardLog()), "mod1", "admin"),
		"/api/v1/moderation/reports/r1/resolve", `{"action":"takedown","note":"контрафакт"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "takedown", fo.gotAction)
}

func TestResolveReport_NotFound_404(t *testing.T) {
	fo := &fakeOps{resolveErr: app.ErrReportNotFound}
	rec := post(t, reportRouter(NewReportHandler(fo, discardLog()), "mod1", "moderator"),
		"/api/v1/moderation/reports/r1/resolve", `{"action":"dismiss"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestResolveReport_Closed_409(t *testing.T) {
	fo := &fakeOps{resolveErr: app.ErrReportClosed}
	rec := post(t, reportRouter(NewReportHandler(fo, discardLog()), "mod1", "moderator"),
		"/api/v1/moderation/reports/r1/resolve", `{"action":"dismiss"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestBan_201(t *testing.T) {
	fo := &fakeOps{}
	rec := post(t, reportRouter(NewReportHandler(fo, discardLog()), "mod1", "admin"),
		"/api/v1/moderation/bans", `{"user_id":"u1","type":"temporary","reason":"спам","duration_seconds":3600}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "u1", fo.gotBanUser)
	assert.Equal(t, time.Hour, fo.gotDuration)
}

func TestBan_MissingUser_422(t *testing.T) {
	fo := &fakeOps{}
	rec := post(t, reportRouter(NewReportHandler(fo, discardLog()), "mod1", "admin"),
		"/api/v1/moderation/bans", `{"type":"permanent"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestBan_InvalidType_422(t *testing.T) {
	fo := &fakeOps{banErr: domain.ErrInvalidBanType}
	rec := post(t, reportRouter(NewReportHandler(fo, discardLog()), "mod1", "admin"),
		"/api/v1/moderation/bans", `{"user_id":"u1","type":"forever"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}
