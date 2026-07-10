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

	"bozor/services/reviews/internal/domain"
	"bozor/services/reviews/internal/repo"
	"bozor/services/reviews/migrations"
)

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_reviews"),
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

func review(adID, authorID, targetID string, rating int) domain.Review {
	now := time.Now().UTC()
	return domain.Review{
		ID: uuid.NewString(), AdID: adID, AuthorID: authorID, TargetID: targetID,
		Rating: rating, Body: "хороший продавец", Status: domain.StatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
}

func createdEvent(t *testing.T, id string) events.Envelope {
	t.Helper()
	e, err := events.New(events.SubjectReviewCreated, "reviews", map[string]any{"review_id": id})
	require.NoError(t, err)
	return e
}

func countOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, events.SubjectReviewCreated).Scan(&n))
	return n
}

func TestReviewsRepo_CreateAndDuplicate(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	adID, buyer, seller := uuid.NewString(), uuid.NewString(), uuid.NewString()
	rev := review(adID, buyer, seller, 5)
	require.NoError(t, r.CreateWithEvent(ctx, rev, createdEvent(t, rev.ID)))
	assert.Equal(t, 1, countOutbox(t, ctx, pool), "bozor.review.created в outbox")

	// Повторный отзыв того же автора по тому же объявлению → ErrDuplicateReview, без события.
	dup := review(adID, buyer, seller, 1)
	err := r.CreateWithEvent(ctx, dup, createdEvent(t, dup.ID))
	assert.ErrorIs(t, err, domain.ErrDuplicateReview)
	assert.Equal(t, 1, countOutbox(t, ctx, pool), "дубликат не плодит событие")

	// Другой автор по тому же объявлению — можно.
	other := review(adID, uuid.NewString(), seller, 4)
	require.NoError(t, r.CreateWithEvent(ctx, other, createdEvent(t, other.ID)))
	assert.Equal(t, 2, countOutbox(t, ctx, pool))
}

func TestReviewsRepo_ListByTargetAndBlock(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	seller := uuid.NewString()
	r1 := review(uuid.NewString(), uuid.NewString(), seller, 5)
	r2 := review(uuid.NewString(), uuid.NewString(), seller, 3)
	require.NoError(t, r.CreateWithEvent(ctx, r1, createdEvent(t, r1.ID)))
	require.NoError(t, r.CreateWithEvent(ctx, r2, createdEvent(t, r2.ID)))
	// Отзыв о другом продавце — не должен попасть в ленту seller.
	rOther := review(uuid.NewString(), uuid.NewString(), uuid.NewString(), 2)
	require.NoError(t, r.CreateWithEvent(ctx, rOther, createdEvent(t, rOther.ID)))

	list, err := r.ListByTarget(ctx, seller, 20, 0)
	require.NoError(t, err)
	require.Len(t, list, 2, "только отзывы о seller")

	// Агрегат рейтинга по активным отзывам seller: (5+3)/2 = 4.0, count=2.
	rt, err := r.AggregateRating(ctx, seller)
	require.NoError(t, err)
	assert.Equal(t, 2, rt.ReviewsCount)
	assert.InDelta(t, 4.0, rt.AvgRating, 0.001)

	// Снятие модератором: отзыв уходит из публичной ленты И из агрегата рейтинга.
	require.NoError(t, r.BlockReview(ctx, r1.ID))
	list, err = r.ListByTarget(ctx, seller, 20, 0)
	require.NoError(t, err)
	require.Len(t, list, 1, "снятый отзыв скрыт из ленты")
	assert.Equal(t, r2.ID, list[0].ID)

	rt, err = r.AggregateRating(ctx, seller)
	require.NoError(t, err)
	assert.Equal(t, 1, rt.ReviewsCount, "снятый отзыв не учитывается в рейтинге")
	assert.InDelta(t, 3.0, rt.AvgRating, 0.001)

	// Нет отзывов — нулевой рейтинг (0/0), не ошибка.
	empty, err := r.AggregateRating(ctx, uuid.NewString())
	require.NoError(t, err)
	assert.Equal(t, 0, empty.ReviewsCount)
	assert.Equal(t, 0.0, empty.AvgRating)

	// Повторное снятие идемпотентно (не ошибка).
	require.NoError(t, r.BlockReview(ctx, r1.ID))
}
