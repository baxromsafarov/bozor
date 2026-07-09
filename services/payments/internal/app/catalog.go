// Package app — прикладные use-cases Payments/Promotions-сервиса. В Stage 8.1
// это сборка публичного каталога услуг с ценами под конкретные регион и категорию.
package app

import (
	"context"
	"log/slog"

	"bozor/services/payments/internal/domain"
)

// Store — доступ к каталогу и ценам (реализуется repo).
type Store interface {
	Services(ctx context.Context) ([]domain.Service, error)
	Bundles(ctx context.Context) ([]domain.Bundle, error)
	PriceRules(ctx context.Context, regionID *int, categoryID *string) ([]domain.PriceRule, error)
}

// PriceOption — цена услуги на конкретную длительность.
type PriceOption struct {
	Duration  int
	AmountUZS int64
}

// PricedService — услуга с ценами по её длительностям (без строки без цены).
type PricedService struct {
	domain.Service
	Options []PriceOption
}

// PricedBundle — набор с итоговой ценой (Priced=false, если цена не задана).
type PricedBundle struct {
	domain.Bundle
	AmountUZS int64
	Priced    bool
}

// Catalog — каталог услуг и наборов с ценами под запрошенные регион/категорию.
type Catalog struct {
	Currency   string
	RegionID   *int
	CategoryID *string
	Services   []PricedService
	Bundles    []PricedBundle
}

// Service — прикладной сервис каталога.
type Service struct {
	store Store
	log   *slog.Logger
}

// NewService создаёт прикладной сервис каталога.
func NewService(store Store, log *slog.Logger) *Service {
	return &Service{store: store, log: log}
}

// GetCatalog собирает каталог услуг/наборов с ценами, разрешёнными под
// (regionID, categoryID). nil-значения означают базовый (общестрановой) прайс.
func (s *Service) GetCatalog(ctx context.Context, regionID *int, categoryID *string) (Catalog, error) {
	services, err := s.store.Services(ctx)
	if err != nil {
		return Catalog{}, err
	}
	bundles, err := s.store.Bundles(ctx)
	if err != nil {
		return Catalog{}, err
	}
	rules, err := s.store.PriceRules(ctx, regionID, categoryID)
	if err != nil {
		return Catalog{}, err
	}
	prices := domain.ResolvePrices(rules)

	cat := Catalog{Currency: domain.CurrencyUZS, RegionID: regionID, CategoryID: categoryID}

	cat.Services = make([]PricedService, 0, len(services))
	for _, svc := range services {
		ps := PricedService{Service: svc}
		for _, d := range svc.Durations {
			if amount, ok := domain.ServicePrice(prices, svc.Code, d); ok {
				ps.Options = append(ps.Options, PriceOption{Duration: d, AmountUZS: amount})
			}
		}
		cat.Services = append(cat.Services, ps)
	}

	cat.Bundles = make([]PricedBundle, 0, len(bundles))
	for _, b := range bundles {
		pb := PricedBundle{Bundle: b}
		if amount, ok := domain.BundlePrice(prices, b.Code); ok {
			pb.AmountUZS, pb.Priced = amount, true
		}
		cat.Bundles = append(cat.Bundles, pb)
	}
	return cat, nil
}
