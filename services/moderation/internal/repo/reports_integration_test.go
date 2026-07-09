//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/moderation/internal/domain"
	"bozor/services/moderation/internal/repo"
)

func TestRepo_Report_CreateListResolve(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	reportID := newID(t)
	adID := newID(t)

	rep := domain.Report{ID: reportID, ReporterID: newID(t), TargetType: domain.TargetAd,
		TargetID: adID, Reason: "контрафакт"}
	ev, err := events.New(events.SubjectModerationReportCreated, "moderation", map[string]string{"report_id": reportID})
	require.NoError(t, err)
	require.NoError(t, r.CreateReportWithEvent(ctx, rep, ev))

	// В очереди open — одна жалоба; событие в outbox.
	open, err := r.ListReports(ctx, domain.ReportOpen, 10)
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, adID, open[0].TargetID)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectModerationReportCreated).Scan(&n))
	assert.Equal(t, 1, n)

	// Разбор takedown с публикацией bozor.ad.blocked.
	rej, err := events.New(events.SubjectAdBlocked, "moderation", map[string]string{"ad_id": adID})
	require.NoError(t, err)
	applied, err := r.ResolveReportWithEvent(ctx, reportID, domain.ReportResolved, "takedown: контрафакт", newID(t), &rej)
	require.NoError(t, err)
	assert.True(t, applied)

	got, found, err := r.GetReport(ctx, reportID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, domain.ReportResolved, got.Status)
	assert.Contains(t, got.Resolution, "takedown")
	require.NotNil(t, got.ResolvedAt)

	// Повторный разбор уже закрытой → applied=false, без нового события.
	applied, err = r.ResolveReportWithEvent(ctx, reportID, domain.ReportDismissed, "dismiss", newID(t), nil)
	require.NoError(t, err)
	assert.False(t, applied)

	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectAdBlocked).Scan(&n))
	assert.Equal(t, 1, n)

	// Очередь open пуста.
	open, err = r.ListReports(ctx, domain.ReportOpen, 10)
	require.NoError(t, err)
	assert.Empty(t, open)
}

func TestRepo_Report_DismissNoEvent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	reportID := newID(t)

	rep := domain.Report{ID: reportID, ReporterID: newID(t), TargetType: domain.TargetUser,
		TargetID: newID(t), Reason: "оскорбление"}
	ev, err := events.New(events.SubjectModerationReportCreated, "moderation", map[string]string{"report_id": reportID})
	require.NoError(t, err)
	require.NoError(t, r.CreateReportWithEvent(ctx, rep, ev))

	applied, err := r.ResolveReportWithEvent(ctx, reportID, domain.ReportDismissed, "dismiss: не подтвердилось", newID(t), nil)
	require.NoError(t, err)
	assert.True(t, applied)

	// Только событие создания — dismiss без события.
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestRepo_CreateBan(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	userID := newID(t)
	exp := time.Now().UTC().Add(24 * time.Hour)

	ban := domain.Ban{ID: newID(t), UserID: userID, Type: domain.BanTemporary,
		Reason: "спам", ExpiresAt: &exp, CreatedBy: newID(t)}
	ev, err := events.New(events.SubjectUserBanned, "moderation", map[string]string{"user_id": userID})
	require.NoError(t, err)
	require.NoError(t, r.CreateBanWithEvent(ctx, ban, ev))

	var (
		gotType string
		gotExp  *time.Time
		n       int
	)
	require.NoError(t, pool.QueryRow(ctx, `SELECT type, expires_at FROM bans WHERE user_id=$1`, userID).Scan(&gotType, &gotExp))
	assert.Equal(t, domain.BanTemporary, gotType)
	require.NotNil(t, gotExp)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectUserBanned).Scan(&n))
	assert.Equal(t, 1, n)
}
