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

	"bozor/services/media/internal/domain"
	"bozor/services/media/internal/repo"
	"bozor/services/media/migrations"
)

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_media"),
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

func uploadedEvent(t *testing.T, mediaID string) events.Envelope {
	t.Helper()
	e, err := events.New(events.SubjectMediaUploaded, "media", map[string]any{"media_id": mediaID})
	require.NoError(t, err)
	return e
}

func mediaEvent(t *testing.T, subject, mediaID string) events.Envelope {
	t.Helper()
	e, err := events.New(subject, "media", map[string]any{"media_id": mediaID})
	require.NoError(t, err)
	return e
}

func ptrInt(v int) *int { return &v }

func sampleMedia(t *testing.T, owner string, adID *string) domain.Media {
	t.Helper()
	id := newID(t)
	return domain.Media{
		ID: id, OwnerUserID: owner, AdID: adID, Bucket: "bozor-media",
		ObjectKey: "originals/" + id + ".png", MimeType: "image/png",
		SizeBytes: 200, Status: domain.StatusUploaded, CreatedAt: time.Now().UTC(),
	}
}

func outboxCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM outbox").Scan(&n))
	return n
}

func TestMediaRepo_InsertGetCount(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	owner := newID(t)
	adID := newID(t)

	m1 := sampleMedia(t, owner, &adID)
	require.NoError(t, r.InsertWithEvent(ctx, m1, uploadedEvent(t, m1.ID)))

	// GetByID возвращает вставленную запись со всеми полями.
	got, err := r.GetByID(ctx, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, owner, got.OwnerUserID)
	require.NotNil(t, got.AdID)
	assert.Equal(t, adID, *got.AdID)
	assert.Equal(t, "image/png", got.MimeType)
	assert.Equal(t, int64(200), got.SizeBytes)
	assert.Equal(t, domain.StatusUploaded, got.Status)
	assert.Equal(t, m1.ObjectKey, got.ObjectKey)
	assert.Nil(t, got.Width, "размеры проставит воркер 3.2")
	assert.Nil(t, got.Height)

	// Вторая картинка того же объявления.
	m2 := sampleMedia(t, owner, &adID)
	require.NoError(t, r.InsertWithEvent(ctx, m2, uploadedEvent(t, m2.ID)))

	// Картинка без привязки к объявлению (ad_id NULL) — не учитывается для adID.
	m3 := sampleMedia(t, owner, nil)
	require.NoError(t, r.InsertWithEvent(ctx, m3, uploadedEvent(t, m3.ID)))

	n, err := r.CountByAd(ctx, adID)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "считаем только медиа с этим ad_id")

	assert.Equal(t, 3, outboxCount(t, pool), "по событию на каждую вставку")
}

func TestMediaRepo_GetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	_, err := r.GetByID(ctx, newID(t))
	assert.ErrorIs(t, err, domain.ErrMediaNotFound)
}

func TestMediaRepo_DuplicateObjectKey_RollsBackEvent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	owner := newID(t)
	m1 := sampleMedia(t, owner, nil)
	require.NoError(t, r.InsertWithEvent(ctx, m1, uploadedEvent(t, m1.ID)))

	// Тот же object_key (UNIQUE) — вставка обязана упасть...
	dup := sampleMedia(t, owner, nil)
	dup.ObjectKey = m1.ObjectKey
	err := r.InsertWithEvent(ctx, dup, uploadedEvent(t, dup.ID))
	require.Error(t, err)

	// ...и транзакция откатывается целиком: лишнего события в outbox не появляется.
	assert.Equal(t, 1, outboxCount(t, pool), "неуспешная вставка не оставляет событие")

	_, err = r.GetByID(ctx, dup.ID)
	assert.ErrorIs(t, err, domain.ErrMediaNotFound)
}

func TestMediaRepo_MarkProcessed_ReadyEventInboxIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	const consumer = "media-processor"

	m := sampleMedia(t, newID(t), nil)
	require.NoError(t, r.InsertWithEvent(ctx, m, uploadedEvent(t, m.ID)))

	m.Width, m.Height = ptrInt(1600), ptrInt(900)
	m.Previews = []domain.Preview{
		{Size: 120, Width: 120, Height: 68, ObjectKey: domain.PreviewKey(m.ID, 120, "jpg")},
		{Size: 480, Width: 480, Height: 270, ObjectKey: domain.PreviewKey(m.ID, 480, "jpg")},
	}
	evID := newID(t)
	up := mediaEvent(t, events.SubjectMediaProcessed, m.ID)
	up.ID = evID
	require.NoError(t, r.MarkProcessedWithEvent(ctx, consumer, evID, m, up))

	// Медиа переведено в ready с размерами и превью.
	got, err := r.GetByID(ctx, m.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusReady, got.Status)
	require.NotNil(t, got.Width)
	assert.Equal(t, 1600, *got.Width)
	require.NotNil(t, got.ProcessedAt)
	require.Len(t, got.Previews, 2)
	assert.Equal(t, 480, got.Previews[1].Size)
	assert.Equal(t, domain.PreviewKey(m.ID, 120, "jpg"), got.Previews[0].ObjectKey)

	// Событие processed в outbox (плюс uploaded от вставки = 2), inbox отметил событие.
	assert.Equal(t, 2, outboxCount(t, pool))
	done, err := r.IsEventProcessed(ctx, consumer, evID)
	require.NoError(t, err)
	assert.True(t, done)

	// Повторная обработка того же события идемпотентна: статус ready, лишнего
	// события не добавилось (переход только из uploaded).
	require.NoError(t, r.MarkProcessedWithEvent(ctx, consumer, evID, m, mediaEvent(t, events.SubjectMediaProcessed, m.ID)))
	assert.Equal(t, 2, outboxCount(t, pool), "повтор не плодит событие")
}

func TestMediaRepo_MarkEventProcessed(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	const consumer = "media-processor"

	evID := newID(t)
	done, err := r.IsEventProcessed(ctx, consumer, evID)
	require.NoError(t, err)
	assert.False(t, done)

	require.NoError(t, r.MarkEventProcessed(ctx, consumer, evID))
	require.NoError(t, r.MarkEventProcessed(ctx, consumer, evID)) // повтор безопасен

	done, err = r.IsEventProcessed(ctx, consumer, evID)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestMediaRepo_ListOrphans(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	cutoff := time.Now().UTC().Add(-24 * time.Hour)

	// Старая сирота (ad_id NULL, создана до cutoff) — кандидат на очистку.
	orphan := sampleMedia(t, newID(t), nil)
	orphan.CreatedAt = cutoff.Add(-time.Hour)
	require.NoError(t, r.InsertWithEvent(ctx, orphan, uploadedEvent(t, orphan.ID)))

	// Старая, но привязанная к объявлению — НЕ сирота.
	adID := newID(t)
	attached := sampleMedia(t, newID(t), &adID)
	attached.CreatedAt = cutoff.Add(-time.Hour)
	require.NoError(t, r.InsertWithEvent(ctx, attached, uploadedEvent(t, attached.ID)))

	// Свежая непривязанная — ещё не сирота (моложе cutoff).
	fresh := sampleMedia(t, newID(t), nil)
	require.NoError(t, r.InsertWithEvent(ctx, fresh, uploadedEvent(t, fresh.ID)))

	orphans, err := r.ListOrphans(ctx, cutoff, 100)
	require.NoError(t, err)
	require.Len(t, orphans, 1, "только старое непривязанное медиа")
	assert.Equal(t, orphan.ID, orphans[0].ID)
}

func TestMediaRepo_DeleteWithEvent(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	m := sampleMedia(t, newID(t), nil)
	require.NoError(t, r.InsertWithEvent(ctx, m, uploadedEvent(t, m.ID)))

	require.NoError(t, r.DeleteWithEvent(ctx, m.ID, mediaEvent(t, events.SubjectMediaDeleted, m.ID)))
	_, err := r.GetByID(ctx, m.ID)
	assert.ErrorIs(t, err, domain.ErrMediaNotFound)

	// Повторное удаление — ErrMediaNotFound (строки уже нет).
	err = r.DeleteWithEvent(ctx, m.ID, mediaEvent(t, events.SubjectMediaDeleted, m.ID))
	assert.ErrorIs(t, err, domain.ErrMediaNotFound)

	// outbox: uploaded + deleted = 2.
	assert.Equal(t, 2, outboxCount(t, pool))
}
