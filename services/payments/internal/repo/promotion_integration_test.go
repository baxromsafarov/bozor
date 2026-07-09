//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"bozor/pkg/shared/migrate"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/repo"
	"bozor/services/payments/migrations"
)

const transportCategory = "a0000000-0000-4000-8000-000000000002"

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("bozor_payments"),
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

// findService возвращает услугу по коду.
func findService(t *testing.T, services []domain.Service, code string) domain.Service {
	t.Helper()
	for _, s := range services {
		if s.Code == code {
			return s
		}
	}
	t.Fatalf("услуга %s не найдена", code)
	return domain.Service{}
}

// TestServices_Seeded — сид услуг загружается с длительностями в порядке отображения.
func TestServices_Seeded(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))

	services, err := r.Services(ctx)
	require.NoError(t, err)
	require.Len(t, services, 4)
	assert.Equal(t, "TOP", services[0].Code, "первым идёт TOP (sort_order)")

	top := findService(t, services, "TOP")
	assert.Equal(t, []int{7, 30}, top.Durations)
	bump := findService(t, services, "BUMP")
	assert.Equal(t, []int{0}, bump.Durations, "BUMP — разовое поднятие")
}

// TestBundles_WithItems — наборы загружаются со своим составом и расписанием BUMP.
func TestBundles_WithItems(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))

	bundles, err := r.Bundles(ctx)
	require.NoError(t, err)
	require.Len(t, bundles, 3)

	byCode := map[string]domain.Bundle{}
	for _, b := range bundles {
		byCode[b.Code] = b
	}

	fast := byCode["FAST_SALE"]
	require.Len(t, fast.Items, 2)
	assert.Equal(t, "TOP", fast.Items[0].ServiceCode)
	assert.Equal(t, 7, fast.Items[0].Duration)
	assert.Equal(t, "BUMP", fast.Items[1].ServiceCode)
	assert.Equal(t, []int{2, 4, 6}, fast.Items[1].BumpSchedule, "расписание авто-поднятий")

	turbo := byCode["TURBO_SALE"]
	require.Len(t, turbo.Items, 3)
	assert.Len(t, turbo.Items[2].BumpSchedule, 9, "9 авто-поднятий")
}

// TestPriceRules_BaseOnly — без региона/категории отбираются только базовые правила.
func TestPriceRules_BaseOnly(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))

	rules, err := r.PriceRules(ctx, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, rules)
	for _, rule := range rules {
		assert.Nil(t, rule.RegionID, "базовое правило без региона")
		assert.Nil(t, rule.CategoryID, "базовое правило без категории")
	}

	prices := domain.ResolvePrices(rules)
	got, ok := domain.ServicePrice(prices, "TOP", 7)
	require.True(t, ok)
	assert.EqualValues(t, 30000, got, "базовая цена TOP/7")
}

// TestPriceRules_ResolvesRegionCategoryOverride — цена TOP/7 в Ташкенте+Транспорте
// разрешается в самое конкретное правило (регион+категория).
func TestPriceRules_ResolvesRegionCategoryOverride(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRepo(startDB(t))

	region := 1 // город Ташкент
	cat := transportCategory

	rules, err := r.PriceRules(ctx, &region, &cat)
	require.NoError(t, err)
	prices := domain.ResolvePrices(rules)

	got, ok := domain.ServicePrice(prices, "TOP", 7)
	require.True(t, ok)
	assert.EqualValues(t, 60000, got, "Ташкент+Транспорт — самое конкретное правило")

	// Только регион (категория другая, не транспорт) → надбавка по региону.
	otherCat := "a0000000-0000-4000-8000-000000000007"
	rules, err = r.PriceRules(ctx, &region, &otherCat)
	require.NoError(t, err)
	got, ok = domain.ServicePrice(domain.ResolvePrices(rules), "TOP", 7)
	require.True(t, ok)
	assert.EqualValues(t, 45000, got, "надбавка только по региону")

	// Только категория (другой регион) → надбавка по категории.
	region2 := 5
	rules, err = r.PriceRules(ctx, &region2, &cat)
	require.NoError(t, err)
	got, ok = domain.ServicePrice(domain.ResolvePrices(rules), "TOP", 7)
	require.True(t, ok)
	assert.EqualValues(t, 40000, got, "надбавка только по категории")
}
