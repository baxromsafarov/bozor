// Package app содержит use-cases Location-сервиса.
package app

import (
	"context"

	"bozor/services/location/internal/domain"
)

// Store — доступ к справочнику (реализуется repo.Repo).
type Store interface {
	Regions(ctx context.Context) ([]domain.Region, error)
	CitiesByRegion(ctx context.Context, regionID int) ([]domain.City, error)
	RegionExists(ctx context.Context, regionID int) (bool, error)
}

// Service — use-cases справочника местоположений.
type Service struct {
	store Store
}

// NewService создаёт use-case-сервис поверх хранилища.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Regions возвращает все регионы.
func (s *Service) Regions(ctx context.Context) ([]domain.Region, error) {
	return s.store.Regions(ctx)
}

// Cities возвращает города региона; для несуществующего региона —
// domain.ErrRegionNotFound (пустой список означает регион без городов).
func (s *Service) Cities(ctx context.Context, regionID int) ([]domain.City, error) {
	exists, err := s.store.RegionExists(ctx, regionID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.ErrRegionNotFound
	}
	return s.store.CitiesByRegion(ctx, regionID)
}
