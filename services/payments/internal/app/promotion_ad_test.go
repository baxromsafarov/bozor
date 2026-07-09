package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/payments/internal/domain"
)

type fakeAds struct {
	ad    domain.AdView
	found bool
	err   error
}

func (f *fakeAds) GetAd(context.Context, string) (domain.AdView, bool, error) {
	return f.ad, f.found, f.err
}

type fakeCatalogSrc struct {
	services []domain.Service
	bundles  []domain.Bundle
	rules    []domain.PriceRule
}

func (f *fakeCatalogSrc) Services(context.Context) ([]domain.Service, error) { return f.services, nil }
func (f *fakeCatalogSrc) Bundles(context.Context) ([]domain.Bundle, error)   { return f.bundles, nil }
func (f *fakeCatalogSrc) PriceRules(context.Context, *int, *string) ([]domain.PriceRule, error) {
	return f.rules, nil
}

type fakeWalletSaga struct {
	debited   int64
	refunded  int64
	debitErr  error
	refundErr error
}

func (f *fakeWalletSaga) Debit(_ context.Context, _ string, amount int64, _ string, _ *string) (domain.Wallet, error) {
	if f.debitErr != nil {
		return domain.Wallet{}, f.debitErr
	}
	f.debited += amount
	return domain.Wallet{}, nil
}
func (f *fakeWalletSaga) Refund(_ context.Context, _ string, amount int64, _ *string, _ string) (domain.Wallet, error) {
	f.refunded += amount
	return domain.Wallet{}, f.refundErr
}

type fakePromoRepo struct {
	activated []domain.AdPromotion
	ev        events.Envelope
	activErr  error
}

func (f *fakePromoRepo) ActivatePromotions(_ context.Context, promos []domain.AdPromotion, ev events.Envelope) error {
	if f.activErr != nil {
		return f.activErr
	}
	f.activated, f.ev = promos, ev
	return nil
}
func (f *fakePromoRepo) ListAdPromotions(context.Context, string, string) ([]domain.AdPromotion, error) {
	return f.activated, nil
}

func sampleCatalog() *fakeCatalogSrc {
	return &fakeCatalogSrc{
		services: []domain.Service{
			{Code: "TOP", Durations: []int{7, 30}},
			{Code: "BUMP", Durations: []int{0}},
		},
		bundles: []domain.Bundle{
			{Code: "FAST_SALE", Items: []domain.BundleItem{
				{ServiceCode: "TOP", Duration: 7},
				{ServiceCode: "BUMP", BumpSchedule: []int{2, 4, 6}},
			}},
		},
		rules: []domain.PriceRule{
			{ProductType: domain.ProductService, ProductCode: "TOP", Duration: 7, AmountUZS: 30000},
			{ProductType: domain.ProductBundle, ProductCode: "FAST_SALE", Duration: 0, AmountUZS: 45000},
		},
	}
}

func activeAd(owner string) domain.AdView {
	return domain.AdView{ID: "ad1", UserID: owner, CategoryID: "cat1", RegionID: 1, Title: "Camry", Status: domain.AdStatusActive}
}

func newSvc(ads *fakeAds, wallet *fakeWalletSaga, repo *fakePromoRepo) *PromotionService {
	return NewPromotionService(ads, sampleCatalog(), wallet, repo, discardLog())
}

func payload(t *testing.T, ev events.Envelope) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(ev.Data, &m))
	return m
}

// TestPromote_SingleService — покупка TOP/7: списание 30000, одна услуга со сроком,
// событие активации с is_top.
func TestPromote_SingleService(t *testing.T) {
	ads := &fakeAds{ad: activeAd("u1"), found: true}
	wallet := &fakeWalletSaga{}
	repo := &fakePromoRepo{}
	svc := newSvc(ads, wallet, repo)

	promos, err := svc.Promote(context.Background(), "u1", "ad1", PromoteRequest{ServiceCode: "TOP", DurationDays: 7})
	require.NoError(t, err)
	require.Len(t, promos, 1)
	assert.Equal(t, "TOP", promos[0].ServiceCode)
	assert.EqualValues(t, 30000, promos[0].AmountUZS)
	require.NotNil(t, promos[0].EndsAt, "TOP имеет срок")
	assert.EqualValues(t, 30000, wallet.debited)
	assert.Zero(t, wallet.refunded)

	assert.Equal(t, events.SubjectPromotionActivated, repo.ev.Type)
	assert.Equal(t, true, payload(t, repo.ev)["is_top"])
}

// TestPromote_Bundle — покупка набора создаёт несколько услуг (TOP+BUMP), списание
// цены набора, у BUMP — расписание.
func TestPromote_Bundle(t *testing.T) {
	ads := &fakeAds{ad: activeAd("u1"), found: true}
	wallet := &fakeWalletSaga{}
	repo := &fakePromoRepo{}
	svc := newSvc(ads, wallet, repo)

	promos, err := svc.Promote(context.Background(), "u1", "ad1", PromoteRequest{BundleCode: "FAST_SALE"})
	require.NoError(t, err)
	require.Len(t, promos, 2)
	assert.EqualValues(t, 45000, wallet.debited)
	assert.EqualValues(t, 45000, promos[0].AmountUZS, "стоимость на первой услуге")
	assert.Zero(t, promos[1].AmountUZS)

	var bump domain.AdPromotion
	for _, p := range promos {
		assert.NotNil(t, p.BundleCode)
		if p.ServiceCode == "BUMP" {
			bump = p
		}
	}
	assert.Equal(t, []int{2, 4, 6}, bump.Schedule, "расписание авто-поднятий")
}

// TestPromote_NotOwner — не владелец не может продвигать; списания нет.
func TestPromote_NotOwner(t *testing.T) {
	ads := &fakeAds{ad: activeAd("owner"), found: true}
	wallet := &fakeWalletSaga{}
	svc := newSvc(ads, wallet, &fakePromoRepo{})

	_, err := svc.Promote(context.Background(), "stranger", "ad1", PromoteRequest{ServiceCode: "TOP", DurationDays: 7})
	assert.ErrorIs(t, err, domain.ErrNotAdOwner)
	assert.Zero(t, wallet.debited)
}

// TestPromote_AdNotFound / NotActive.
func TestPromote_AdNotFoundAndInactive(t *testing.T) {
	svc := newSvc(&fakeAds{found: false}, &fakeWalletSaga{}, &fakePromoRepo{})
	_, err := svc.Promote(context.Background(), "u1", "ad1", PromoteRequest{ServiceCode: "TOP", DurationDays: 7})
	assert.ErrorIs(t, err, domain.ErrAdNotFound)

	draft := activeAd("u1")
	draft.Status = "draft"
	svc2 := newSvc(&fakeAds{ad: draft, found: true}, &fakeWalletSaga{}, &fakePromoRepo{})
	_, err = svc2.Promote(context.Background(), "u1", "ad1", PromoteRequest{ServiceCode: "TOP", DurationDays: 7})
	assert.ErrorIs(t, err, domain.ErrAdNotPromotable)
}

// TestPromote_InsufficientFunds — недостаток средств не активирует услугу.
func TestPromote_InsufficientFunds(t *testing.T) {
	ads := &fakeAds{ad: activeAd("u1"), found: true}
	wallet := &fakeWalletSaga{debitErr: domain.ErrInsufficientFunds}
	repo := &fakePromoRepo{}
	svc := newSvc(ads, wallet, repo)

	_, err := svc.Promote(context.Background(), "u1", "ad1", PromoteRequest{ServiceCode: "TOP", DurationDays: 7})
	assert.ErrorIs(t, err, domain.ErrInsufficientFunds)
	assert.Nil(t, repo.activated, "активации не было")
}

// TestPromote_CompensatesOnActivationFailure — сбой активации откатывается
// возвратом средств (компенсация саги).
func TestPromote_CompensatesOnActivationFailure(t *testing.T) {
	ads := &fakeAds{ad: activeAd("u1"), found: true}
	wallet := &fakeWalletSaga{}
	repo := &fakePromoRepo{activErr: errors.New("db error")}
	svc := newSvc(ads, wallet, repo)

	_, err := svc.Promote(context.Background(), "u1", "ad1", PromoteRequest{ServiceCode: "TOP", DurationDays: 7})
	require.Error(t, err)
	assert.EqualValues(t, 30000, wallet.debited)
	assert.EqualValues(t, 30000, wallet.refunded, "средства возвращены компенсацией")
}

// TestPromote_InvalidDuration — недопустимая длительность отклоняется до списания.
func TestPromote_InvalidDuration(t *testing.T) {
	ads := &fakeAds{ad: activeAd("u1"), found: true}
	wallet := &fakeWalletSaga{}
	svc := newSvc(ads, wallet, &fakePromoRepo{})

	_, err := svc.Promote(context.Background(), "u1", "ad1", PromoteRequest{ServiceCode: "TOP", DurationDays: 3})
	assert.ErrorIs(t, err, domain.ErrInvalidDuration)
	assert.Zero(t, wallet.debited)
}

// TestPromote_EmptyRequest — без услуги и набора → ошибка.
func TestPromote_EmptyRequest(t *testing.T) {
	ads := &fakeAds{ad: activeAd("u1"), found: true}
	svc := newSvc(ads, &fakeWalletSaga{}, &fakePromoRepo{})

	_, err := svc.Promote(context.Background(), "u1", "ad1", PromoteRequest{})
	assert.ErrorIs(t, err, domain.ErrEmptyPromotion)
}
