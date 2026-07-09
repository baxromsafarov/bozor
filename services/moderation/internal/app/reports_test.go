package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/moderation/internal/app"
	"bozor/services/moderation/internal/domain"
)

type fakeOpsStore struct {
	report  domain.Report
	found   bool
	applied bool

	createdReport      *domain.Report
	createdReportType  string
	resolvedStatus     string
	resolvedResolution string
	resolveEventType   string // "" если события не было
	createdBan         *domain.Ban
	banPayload         map[string]any
}

func (s *fakeOpsStore) CreateReportWithEvent(_ context.Context, rep domain.Report, ev events.Envelope) error {
	r := rep
	s.createdReport = &r
	s.createdReportType = ev.Type
	return nil
}
func (s *fakeOpsStore) GetReport(context.Context, string) (domain.Report, bool, error) {
	return s.report, s.found, nil
}
func (s *fakeOpsStore) ResolveReportWithEvent(_ context.Context, _, status, resolution, _ string, ev *events.Envelope) (bool, error) {
	s.resolvedStatus, s.resolvedResolution = status, resolution
	if ev != nil {
		s.resolveEventType = ev.Type
	}
	return s.applied, nil
}
func (s *fakeOpsStore) ListReports(context.Context, string, int) ([]domain.Report, error) {
	return nil, nil
}
func (s *fakeOpsStore) CreateBanWithEvent(_ context.Context, ban domain.Ban, ev events.Envelope) error {
	b := ban
	s.createdBan = &b
	s.banPayload = map[string]any{}
	_ = ev.Decode(&s.banPayload)
	return nil
}

type fakeAdSource struct {
	ad    domain.AdView
	found bool
}

func (s fakeAdSource) GetAd(context.Context, string) (domain.AdView, bool, error) {
	return s.ad, s.found, nil
}

func opsSvc(store *fakeOpsStore, src fakeAdSource) *app.OpsService {
	return app.NewOpsService(store, src, discardLog())
}

func TestCreateReport(t *testing.T) {
	store := &fakeOpsStore{}
	rep, err := opsSvc(store, fakeAdSource{}).CreateReport(context.Background(), "u1", domain.TargetAd, "ad1", "спам")
	require.NoError(t, err)
	assert.Equal(t, domain.ReportOpen, rep.Status)
	require.NotNil(t, store.createdReport)
	assert.Equal(t, events.SubjectModerationReportCreated, store.createdReportType)
}

func TestCreateReport_Invalid(t *testing.T) {
	store := &fakeOpsStore{}
	_, err := opsSvc(store, fakeAdSource{}).CreateReport(context.Background(), "u1", "planet", "x", "спам")
	assert.ErrorIs(t, err, domain.ErrInvalidTarget)
	assert.Nil(t, store.createdReport)
}

func TestResolveReport_Dismiss_NoEvent(t *testing.T) {
	store := &fakeOpsStore{report: domain.Report{Status: domain.ReportOpen, TargetType: domain.TargetUser}, found: true, applied: true}
	require.NoError(t, opsSvc(store, fakeAdSource{}).ResolveReport(context.Background(), "r1", "mod1", domain.ActionDismiss, ""))
	assert.Equal(t, domain.ReportDismissed, store.resolvedStatus)
	assert.Empty(t, store.resolveEventType) // без события
}

func TestResolveReport_Takedown_PublishesBlock(t *testing.T) {
	store := &fakeOpsStore{report: domain.Report{Status: domain.ReportOpen, TargetType: domain.TargetAd, TargetID: "ad1"}, found: true, applied: true}
	src := fakeAdSource{ad: domain.AdView{ID: "ad1", UserID: "owner1", Title: "Копия"}, found: true}
	require.NoError(t, opsSvc(store, src).ResolveReport(context.Background(), "r1", "mod1", domain.ActionTakedown, "контрафакт"))
	assert.Equal(t, domain.ReportResolved, store.resolvedStatus)
	assert.Equal(t, events.SubjectAdBlocked, store.resolveEventType)
	assert.Contains(t, store.resolvedResolution, "контрафакт")
}

func TestResolveReport_TakedownOnUser_Rejected(t *testing.T) {
	store := &fakeOpsStore{report: domain.Report{Status: domain.ReportOpen, TargetType: domain.TargetUser}, found: true}
	err := opsSvc(store, fakeAdSource{}).ResolveReport(context.Background(), "r1", "mod1", domain.ActionTakedown, "x")
	assert.ErrorIs(t, err, domain.ErrTakedownTarget)
}

func TestResolveReport_NotFound(t *testing.T) {
	store := &fakeOpsStore{found: false}
	assert.ErrorIs(t, opsSvc(store, fakeAdSource{}).ResolveReport(context.Background(), "r1", "mod1", domain.ActionDismiss, ""), app.ErrReportNotFound)
}

func TestResolveReport_AlreadyClosed(t *testing.T) {
	store := &fakeOpsStore{report: domain.Report{Status: domain.ReportResolved}, found: true}
	assert.ErrorIs(t, opsSvc(store, fakeAdSource{}).ResolveReport(context.Background(), "r1", "mod1", domain.ActionDismiss, ""), app.ErrReportClosed)
}

func TestResolveReport_RaceLost(t *testing.T) {
	store := &fakeOpsStore{report: domain.Report{Status: domain.ReportOpen, TargetType: domain.TargetUser}, found: true, applied: false}
	assert.ErrorIs(t, opsSvc(store, fakeAdSource{}).ResolveReport(context.Background(), "r1", "mod1", domain.ActionWarn, ""), app.ErrReportClosed)
}

func TestBanUser_Temporary(t *testing.T) {
	store := &fakeOpsStore{}
	ban, err := opsSvc(store, fakeAdSource{}).BanUser(context.Background(), "mod1", "u1", domain.BanTemporary, "спам", time.Hour)
	require.NoError(t, err)
	require.NotNil(t, ban.ExpiresAt)
	require.NotNil(t, store.createdBan)
	assert.Equal(t, "u1", store.banPayload["user_id"])
	assert.Equal(t, domain.BanTemporary, store.banPayload["type"])
}

func TestBanUser_Permanent_NoExpiry(t *testing.T) {
	store := &fakeOpsStore{}
	ban, err := opsSvc(store, fakeAdSource{}).BanUser(context.Background(), "mod1", "u1", domain.BanPermanent, "мошенник", 0)
	require.NoError(t, err)
	assert.Nil(t, ban.ExpiresAt)
}

func TestBanUser_Invalid(t *testing.T) {
	store := &fakeOpsStore{}
	_, err := opsSvc(store, fakeAdSource{}).BanUser(context.Background(), "mod1", "u1", domain.BanTemporary, "", 0)
	assert.ErrorIs(t, err, domain.ErrBanDurationRequired)
	assert.Nil(t, store.createdBan)
}
