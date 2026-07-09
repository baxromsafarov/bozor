package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/payments/internal/domain"
)

// hoursPerDay — длительность услуги задаётся в днях.
const hoursPerDay = 24 * time.Hour

// Ads — чтение объявления из Listing (владелец, регион/категория, статус).
type Ads interface {
	GetAd(ctx context.Context, id string) (domain.AdView, bool, error)
}

// CatalogSource — каталог услуг и цены (для разрешения стоимости покупки).
type CatalogSource interface {
	Services(ctx context.Context) ([]domain.Service, error)
	Bundles(ctx context.Context) ([]domain.Bundle, error)
	PriceRules(ctx context.Context, regionID *int, categoryID *string) ([]domain.PriceRule, error)
}

// WalletSaga — операции кошелька в саге покупки (списание и компенсирующий возврат).
type WalletSaga interface {
	Debit(ctx context.Context, userID string, amount int64, kind string, reference *string) (domain.Wallet, error)
	Refund(ctx context.Context, userID string, amount int64, reference *string, reason string) (domain.Wallet, error)
}

// PromotionRepo — персистентность применённых услуг.
type PromotionRepo interface {
	ActivatePromotions(ctx context.Context, promos []domain.AdPromotion, ev events.Envelope) error
	ListAdPromotions(ctx context.Context, adID, status string) ([]domain.AdPromotion, error)
}

// PromoteRequest — запрос на продвижение: либо услуга с длительностью, либо набор.
type PromoteRequest struct {
	ServiceCode  string
	DurationDays int
	BundleCode   string
}

// PromotionService применяет платные услуги к объявлению по саге
// списание→активация→компенсация.
type PromotionService struct {
	ads     Ads
	catalog CatalogSource
	wallet  WalletSaga
	repo    PromotionRepo
	log     *slog.Logger
}

// NewPromotionService создаёт сервис применения услуг.
func NewPromotionService(ads Ads, catalog CatalogSource, wallet WalletSaga, repo PromotionRepo, log *slog.Logger) *PromotionService {
	return &PromotionService{ads: ads, catalog: catalog, wallet: wallet, repo: repo, log: log}
}

// Promote применяет услугу/набор к объявлению. Сага: (1) списание с кошелька,
// (2) создание ad_promotions + bozor.promotion.activated, (3) при сбое активации —
// компенсирующий возврат средств (bozor.wallet.refunded).
func (s *PromotionService) Promote(ctx context.Context, userID, adID string, req PromoteRequest) ([]domain.AdPromotion, error) {
	ad, found, err := s.ads.GetAd(ctx, adID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, domain.ErrAdNotFound
	}
	if ad.UserID != userID {
		return nil, domain.ErrNotAdOwner
	}
	if ad.Status != domain.AdStatusActive {
		return nil, domain.ErrAdNotPromotable
	}

	items, amount, primary, isTop, bundleCode, err := s.buildPlan(ctx, req, ad)
	if err != nil {
		return nil, err
	}

	// Сага, шаг 1 — списание с кошелька (локальная транзакция + событие в 8.2/леджер).
	if _, err := s.wallet.Debit(ctx, userID, amount, domain.KindPurchase, &adID); err != nil {
		return nil, err
	}

	// Строки услуг: полная стоимость относится на первую (для пропорц. возврата 8.7).
	now := time.Now().UTC()
	promos := make([]domain.AdPromotion, len(items))
	var topEndsAt *time.Time
	for i, it := range items {
		amt := int64(0)
		if i == 0 {
			amt = amount
		}
		promos[i] = domain.AdPromotion{
			ID: uuid.New().String(), AdID: adID, UserID: userID,
			ServiceCode: it.ServiceCode, BundleCode: bundleCode, Status: domain.PromotionActive,
			AmountUZS: amt, StartsAt: now, EndsAt: it.EndsAt, Schedule: it.Schedule,
		}
		if domain.IsTopService(it.ServiceCode) {
			topEndsAt = it.EndsAt
		}
	}

	ev, err := s.activatedEvent(ad, primary, isTop, topEndsAt)
	if err != nil {
		return nil, err
	}

	// Сага, шаг 2 — активация. При сбое — компенсация (возврат средств).
	if err := s.repo.ActivatePromotions(ctx, promos, ev); err != nil {
		if _, rerr := s.wallet.Refund(ctx, userID, amount, &adID, "activation_failed"); rerr != nil {
			s.log.ErrorContext(ctx, "компенсация возврата не удалась", slog.String("ad_id", adID),
				slog.String("error", rerr.Error()))
		}
		return nil, fmt.Errorf("активация услуги не удалась (средства возвращены): %w", err)
	}
	return promos, nil
}

// Promotions возвращает активные услуги объявления.
func (s *PromotionService) Promotions(ctx context.Context, adID string) ([]domain.AdPromotion, error) {
	return s.repo.ListAdPromotions(ctx, adID, domain.PromotionActive)
}

// buildPlan разрешает стоимость под регион/категорию объявления и строит план
// применения (список услуг со сроками/расписанием). Возвращает также итоговую
// сумму, primary-услугу (для события), флаг is_top и код набора (nil для одиночной).
func (s *PromotionService) buildPlan(ctx context.Context, req PromoteRequest, ad domain.AdView) ([]domain.PromotionItem, int64, string, bool, *string, error) {
	region := ad.RegionID
	rules, err := s.catalog.PriceRules(ctx, &region, &ad.CategoryID)
	if err != nil {
		return nil, 0, "", false, nil, err
	}
	prices := domain.ResolvePrices(rules)
	now := time.Now().UTC()

	switch {
	case req.BundleCode != "":
		return s.bundlePlan(ctx, req.BundleCode, prices, now)
	case req.ServiceCode != "":
		return s.servicePlan(ctx, req.ServiceCode, req.DurationDays, prices, now)
	default:
		return nil, 0, "", false, nil, domain.ErrEmptyPromotion
	}
}

func (s *PromotionService) servicePlan(ctx context.Context, code string, duration int, prices map[domain.PriceKey]int64, now time.Time) ([]domain.PromotionItem, int64, string, bool, *string, error) {
	services, err := s.catalog.Services(ctx)
	if err != nil {
		return nil, 0, "", false, nil, err
	}
	svc, ok := findService(services, code)
	if !ok {
		return nil, 0, "", false, nil, domain.ErrUnknownService
	}
	if !containsInt(svc.Durations, duration) {
		return nil, 0, "", false, nil, domain.ErrInvalidDuration
	}
	amount, ok := domain.ServicePrice(prices, code, duration)
	if !ok {
		return nil, 0, "", false, nil, domain.ErrNoPrice
	}
	item := domain.PromotionItem{ServiceCode: code}
	if code == domain.ServiceBump {
		item.Schedule = []int{0} // разовое поднятие «сразу» (исполняет воркер 8.5)
	} else {
		item.EndsAt = endsAt(now, duration)
	}
	return []domain.PromotionItem{item}, amount, code, domain.IsTopService(code), nil, nil
}

func (s *PromotionService) bundlePlan(ctx context.Context, code string, prices map[domain.PriceKey]int64, now time.Time) ([]domain.PromotionItem, int64, string, bool, *string, error) {
	bundles, err := s.catalog.Bundles(ctx)
	if err != nil {
		return nil, 0, "", false, nil, err
	}
	bundle, ok := findBundle(bundles, code)
	if !ok {
		return nil, 0, "", false, nil, domain.ErrUnknownBundle
	}
	amount, ok := domain.BundlePrice(prices, code)
	if !ok {
		return nil, 0, "", false, nil, domain.ErrNoPrice
	}

	items := make([]domain.PromotionItem, 0, len(bundle.Items))
	primary, isTop := "", false
	for _, bi := range bundle.Items {
		it := domain.PromotionItem{ServiceCode: bi.ServiceCode}
		if bi.ServiceCode == domain.ServiceBump {
			it.Schedule = bi.BumpSchedule
		} else {
			it.EndsAt = endsAt(now, bi.Duration)
		}
		items = append(items, it)
		if domain.IsTopService(bi.ServiceCode) {
			primary, isTop = bi.ServiceCode, true
		}
	}
	if primary == "" && len(bundle.Items) > 0 {
		primary = bundle.Items[0].ServiceCode
	}
	bundleCode := code
	return items, amount, primary, isTop, &bundleCode, nil
}

// promotionActivatedPayload — событие bozor.promotion.activated (Notification
// читает title; Search — is_top/promotion_rank/ends_at для топ-блока, 8.6).
type promotionActivatedPayload struct {
	AdID          string     `json:"ad_id"`
	UserID        string     `json:"user_id"`
	Title         string     `json:"title"`
	ServiceCode   string     `json:"service_code"`
	IsTop         bool       `json:"is_top"`
	PromotionRank int64      `json:"promotion_rank"`
	EndsAt        *time.Time `json:"ends_at,omitempty"`
}

func (s *PromotionService) activatedEvent(ad domain.AdView, primary string, isTop bool, topEndsAt *time.Time) (events.Envelope, error) {
	var rank int64
	if isTop && topEndsAt != nil {
		rank = topEndsAt.Unix() // позже истекает — выше в топ-блоке (8.6 уточнит)
	}
	return events.New(events.SubjectPromotionActivated, "payments", promotionActivatedPayload{
		AdID: ad.ID, UserID: ad.UserID, Title: ad.Title, ServiceCode: primary,
		IsTop: isTop, PromotionRank: rank, EndsAt: topEndsAt,
	})
}

func endsAt(now time.Time, days int) *time.Time {
	t := now.Add(time.Duration(days) * hoursPerDay)
	return &t
}

func findService(services []domain.Service, code string) (domain.Service, bool) {
	for _, s := range services {
		if s.Code == code {
			return s, true
		}
	}
	return domain.Service{}, false
}

func findBundle(bundles []domain.Bundle, code string) (domain.Bundle, bool) {
	for _, b := range bundles {
		if b.Code == code {
			return b, true
		}
	}
	return domain.Bundle{}, false
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
