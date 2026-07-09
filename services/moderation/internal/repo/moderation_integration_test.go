//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"bozor/pkg/shared/events"
	"bozor/pkg/shared/migrate"

	"bozor/services/moderation/internal/domain"
	"bozor/services/moderation/internal/repo"
	"bozor/services/moderation/migrations"
)

const consumer = "moderation-auto"

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_moderation"),
		tcpostgres.WithUsername("bozor"),
		tcpostgres.WithPassword("bozor"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	_, err = migrate.Up(ctx, dsn, migrations.FS)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}

func TestRepo_StopwordsSeeded(t *testing.T) {
	r := repo.NewRepo(startDB(t))
	words, err := r.ActiveStopwords(context.Background())
	require.NoError(t, err)
	assert.Contains(t, words, "копия")
	assert.Contains(t, words, "под оригинал")
	assert.Contains(t, words, "soxta")
	assert.GreaterOrEqual(t, len(words), 7)
}

func TestRepo_ForbiddenCategory(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	cat := newID(t)

	ok, err := r.IsForbiddenCategory(ctx, cat)
	require.NoError(t, err)
	assert.False(t, ok)

	_, err = pool.Exec(ctx, `INSERT INTO forbidden_categories (category_id, reason) VALUES ($1,'оружие')`, cat)
	require.NoError(t, err)

	ok, err = r.IsForbiddenCategory(ctx, cat)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRepo_ApprovedTask_Inbox_Outbox(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	eventID := newID(t)
	adID := newID(t)

	task := domain.Task{ID: newID(t), AdID: adID, UserID: newID(t), Title: "iPhone",
		ContentHash: domain.ContentHash("iPhone", "новый"), Status: domain.StatusApproved,
		AutoResult: domain.AutoPassed, Reasons: []string{}}
	ev, err := events.New(events.SubjectAdApproved, "moderation", map[string]string{"ad_id": adID})
	require.NoError(t, err)

	require.NoError(t, r.SaveApprovedTask(ctx, consumer, eventID, task, ev))

	// Событие отмечено обработанным (inbox).
	processed, err := r.AlreadyProcessed(ctx, consumer, eventID)
	require.NoError(t, err)
	assert.True(t, processed)

	// bozor.ad.approved лежит в outbox.
	var outboxN int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectAdApproved).Scan(&outboxN))
	assert.Equal(t, 1, outboxN)

	// Задача одобрена.
	list, err := r.ListTasks(ctx, domain.StatusApproved, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, adID, list[0].AdID)
	assert.Empty(t, list[0].Reasons)
}

func TestRepo_ManualTask_And_Dedup(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	userID := newID(t)
	hash := domain.ContentHash("Копия iPhone", "реплика")

	adID := newID(t)
	task := domain.Task{ID: newID(t), AdID: adID, UserID: userID, Title: "Копия iPhone",
		ContentHash: hash, Status: domain.StatusManual, AutoResult: domain.AutoFlagged,
		Reasons: []string{"stopword:копия"}}
	require.NoError(t, r.SaveManualTask(ctx, consumer, newID(t), task))

	// Детекция дублей: другое объявление того же пользователя с тем же хэшем.
	dup, err := r.HasDuplicate(ctx, userID, hash, newID(t))
	require.NoError(t, err)
	assert.True(t, dup)

	// Само объявление не считается своим дублем.
	self, err := r.HasDuplicate(ctx, userID, hash, adID)
	require.NoError(t, err)
	assert.False(t, self)

	list, err := r.ListTasks(ctx, domain.StatusManual, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, []string{"stopword:копия"}, list[0].Reasons)
}

func TestRepo_UpsertByAdID_Remoderation(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	adID := newID(t)
	userID := newID(t)

	// Первая модерация — в ручную очередь.
	manual := domain.Task{ID: newID(t), AdID: adID, UserID: userID, Title: "Копия",
		ContentHash: "h1", Status: domain.StatusManual, AutoResult: domain.AutoFlagged, Reasons: []string{"stopword:копия"}}
	require.NoError(t, r.SaveManualTask(ctx, consumer, newID(t), manual))

	// Правка прошла проверки — та же задача (по ad_id) становится approved.
	ev, err := events.New(events.SubjectAdApproved, "moderation", map[string]string{"ad_id": adID})
	require.NoError(t, err)
	approved := domain.Task{ID: newID(t), AdID: adID, UserID: userID, Title: "Оригинал",
		ContentHash: "h2", Status: domain.StatusApproved, AutoResult: domain.AutoPassed, Reasons: []string{}}
	require.NoError(t, r.SaveApprovedTask(ctx, consumer, newID(t), approved, ev))

	// Ручная очередь пуста, задача одобрена (одна строка на ad_id).
	manualList, err := r.ListTasks(ctx, domain.StatusManual, 10)
	require.NoError(t, err)
	assert.Empty(t, manualList)
	approvedList, err := r.ListTasks(ctx, domain.StatusApproved, 10)
	require.NoError(t, err)
	require.Len(t, approvedList, 1)
	assert.Equal(t, "Оригинал", approvedList[0].Title)
}
