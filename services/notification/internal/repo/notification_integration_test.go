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

	"bozor/pkg/shared/migrate"

	"bozor/services/notification/internal/domain"
	"bozor/services/notification/internal/repo"
	"bozor/services/notification/migrations"
)

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_notification"),
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

func TestRepo_RecipientUpsert(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)

	_, found, err := r.GetRecipient(ctx, uid)
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, r.UpsertRecipient(ctx, domain.Recipient{UserID: uid, TelegramUserID: 111, LanguageCode: "ru"}))
	// Повторный upsert обновляет chat_id/язык (идемпотентно по user_id).
	require.NoError(t, r.UpsertRecipient(ctx, domain.Recipient{UserID: uid, TelegramUserID: 222, LanguageCode: "uz"}))

	rec, found, err := r.GetRecipient(ctx, uid)
	require.NoError(t, err)
	require.True(t, found)
	assert.EqualValues(t, 222, rec.TelegramUserID)
	assert.Equal(t, "uz", rec.LanguageCode)
}

func TestRepo_DeliveryLifecycle(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)
	eventID := newID(t)
	payload := []byte(`{"user_id":"` + uid + `","title":"X"}`)

	// Первая регистрация доставки → pending, proceed=true.
	proceed, err := r.BeginDelivery(ctx, newID(t), eventID, uid, "bozor.ad.approved", payload, "текст")
	require.NoError(t, err)
	assert.True(t, proceed)

	// Повторная (redelivery) пока pending → всё ещё proceed=true, attempts++.
	proceed, err = r.BeginDelivery(ctx, newID(t), eventID, uid, "bozor.ad.approved", payload, "текст")
	require.NoError(t, err)
	assert.True(t, proceed)

	require.NoError(t, r.MarkSent(ctx, eventID))

	// После sent повторная доставка не проходит (идемпотентность).
	proceed, err = r.BeginDelivery(ctx, newID(t), eventID, uid, "bozor.ad.approved", payload, "текст")
	require.NoError(t, err)
	assert.False(t, proceed)

	list, err := r.ListByUser(ctx, uid, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, domain.StatusSent, list[0].Status)
	assert.Equal(t, 3, list[0].Attempts) // три регистрации
	require.NotNil(t, list[0].SentAt)
	assert.Equal(t, "текст", list[0].Body)
}

func TestRepo_RecordSkippedIdempotent(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)
	eventID := newID(t)

	require.NoError(t, r.RecordSkipped(ctx, newID(t), eventID, uid, "bozor.saved_search.matched",
		[]byte(`{}`), domain.ReasonPrefsDisabled))
	// Повтор по тому же event_id не создаёт дубля.
	require.NoError(t, r.RecordSkipped(ctx, newID(t), eventID, uid, "bozor.saved_search.matched",
		[]byte(`{}`), domain.ReasonPrefsDisabled))

	list, err := r.ListByUser(ctx, uid, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, domain.StatusSkipped, list[0].Status)
	assert.Equal(t, domain.ReasonPrefsDisabled, list[0].Reason)
}

func TestRepo_MarkFailedAndSkipped(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)

	e1 := newID(t)
	_, err := r.BeginDelivery(ctx, newID(t), e1, uid, "bozor.ad.approved", []byte(`{}`), "t")
	require.NoError(t, err)
	require.NoError(t, r.MarkFailed(ctx, e1, "permanent_error: blocked"))

	e2 := newID(t)
	_, err = r.BeginDelivery(ctx, newID(t), e2, uid, "bozor.ad.expired", []byte(`{}`), "t")
	require.NoError(t, err)
	require.NoError(t, r.MarkSkipped(ctx, e2, domain.ReasonChannelDisabled))

	list, err := r.ListByUser(ctx, uid, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	byStatus := map[string]domain.Notification{}
	for _, n := range list {
		byStatus[n.Status] = n
	}
	assert.Contains(t, byStatus[domain.StatusFailed].Reason, "blocked")
	assert.Equal(t, domain.ReasonChannelDisabled, byStatus[domain.StatusSkipped].Reason)
}
