//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/favorites-savedsearch/internal/domain"
	"bozor/services/favorites-savedsearch/internal/repo"
)

func i64(v int64) *int64 { return &v }

func seedSavedSearch(t *testing.T, ctx context.Context, r *repo.Repo, userID string, q domain.SearchQuery) domain.SavedSearch {
	t.Helper()
	ss := domain.SavedSearch{
		ID: newID(t), UserID: userID, Name: "поиск", Query: q,
		NotifyEnabled: true, CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, r.CreateSavedSearch(ctx, ss))
	return ss
}

func matchedEvent(t *testing.T, ssID, adID string) events.Envelope {
	t.Helper()
	e, err := events.New(events.SubjectSavedSearchMatched, "favorites-savedsearch",
		map[string]string{"saved_search_id": ssID, "ad_id": adID})
	require.NoError(t, err)
	return e
}

func TestRepo_SavedSearch_CRUD(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)

	ss := seedSavedSearch(t, ctx, r, uid, domain.SearchQuery{CategoryID: newID(t), RegionID: 14, PriceMax: i64(5000)})

	n, err := r.CountSavedSearches(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	list, err := r.ListSavedSearches(ctx, uid)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, ss.ID, list[0].ID)
	assert.Equal(t, ss.Query.CategoryID, list[0].Query.CategoryID, "query_json сохранён и разобран")
	require.NotNil(t, list[0].Query.PriceMax)
	assert.Equal(t, int64(5000), *list[0].Query.PriceMax)

	existed, err := r.DeleteSavedSearch(ctx, ss.ID, uid)
	require.NoError(t, err)
	assert.True(t, existed)
	// Чужой/повтор — не существует.
	existed, err = r.DeleteSavedSearch(ctx, ss.ID, uid)
	require.NoError(t, err)
	assert.False(t, existed)
}

func TestRepo_Candidates_CoarseKeys(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)
	cars := newID(t)

	ssCars := seedSavedSearch(t, ctx, r, uid, domain.SearchQuery{CategoryID: cars}) // категория cars
	ssAny := seedSavedSearch(t, ctx, r, uid, domain.SearchQuery{})                  // любая
	ssRegion := seedSavedSearch(t, ctx, r, uid, domain.SearchQuery{RegionID: 14})   // регион 14
	_ = seedSavedSearch(t, ctx, r, uid, domain.SearchQuery{CategoryID: newID(t)})   // другая категория

	// Объявление cars/регион 14 → кандидаты: cars, any, region (не «другая категория»).
	cands, err := r.Candidates(ctx, cars, 14)
	require.NoError(t, err)
	ids := map[string]bool{}
	for _, c := range cands {
		ids[c.ID] = true
	}
	assert.True(t, ids[ssCars.ID], "категория cars")
	assert.True(t, ids[ssAny.ID], "любая категория/регион")
	assert.True(t, ids[ssRegion.ID], "регион 14")
	assert.Len(t, cands, 3, "поиск другой категории отсеян грубым ключом")
}

func TestRepo_Candidates_ExcludesNotifyDisabled(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))
	uid := newID(t)
	ss := domain.SavedSearch{ID: newID(t), UserID: uid, Name: "off", Query: domain.SearchQuery{}, NotifyEnabled: false, CreatedAt: time.Now().UTC()}
	require.NoError(t, r.CreateSavedSearch(ctx, ss))

	cands, err := r.Candidates(ctx, newID(t), 1)
	require.NoError(t, err)
	assert.Empty(t, cands, "выключенные уведомления не попадают в кандидаты")
}

func TestRepo_RecordMatchWithEvent_DedupAndOutbox(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	uid := newID(t)
	ss := seedSavedSearch(t, ctx, r, uid, domain.SearchQuery{})
	adID := newID(t)

	// Первое совпадение — публикуется (throttle 0 → без подавления).
	published, err := r.RecordMatchWithEvent(ctx, ss.ID, adID, 0, matchedEvent(t, ss.ID, adID))
	require.NoError(t, err)
	assert.True(t, published)

	// Повтор той же пары — дедуп, не публикуется.
	published, err = r.RecordMatchWithEvent(ctx, ss.ID, adID, 0, matchedEvent(t, ss.ID, adID))
	require.NoError(t, err)
	assert.False(t, published, "пара уже уведомлена")

	// В outbox ровно одно событие.
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE subject = $1`, events.SubjectSavedSearchMatched).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestRepo_RecordMatchWithEvent_Throttled(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)
	uid := newID(t)
	ss := seedSavedSearch(t, ctx, r, uid, domain.SearchQuery{})

	// Первое совпадение с большим окном троттлинга — публикуется, last_notified_at выставлен.
	published, err := r.RecordMatchWithEvent(ctx, ss.ID, newID(t), 3600, matchedEvent(t, ss.ID, "a"))
	require.NoError(t, err)
	assert.True(t, published)

	// Второе совпадение (другое объявление) в пределах окна — подавлено троттлингом.
	published, err = r.RecordMatchWithEvent(ctx, ss.ID, newID(t), 3600, matchedEvent(t, ss.ID, "b"))
	require.NoError(t, err)
	assert.False(t, published, "подавлено троттлингом (защита от лавины)")

	// В outbox — только первое.
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&n))
	assert.Equal(t, 1, n)
}
