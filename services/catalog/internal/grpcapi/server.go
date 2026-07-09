// Package grpcapi реализует gRPC-контракт Catalog (catalog.v1.CatalogService)
// для синхронных межсервисных вызовов — прежде всего валидации атрибутов
// объявления Listing-сервисом.
package grpcapi

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "bozor/pkg/shared/pb/catalog/v1"

	"bozor/services/catalog/internal/domain"
)

// AttributeReader отдаёт эффективный набор атрибутов категории
// (реализуется app.AttributeService).
type AttributeReader interface {
	Effective(ctx context.Context, categoryID string) ([]domain.EffectiveAttribute, error)
}

// Server реализует catalog.v1.CatalogService поверх use-cases атрибутов.
type Server struct {
	catalogv1.UnimplementedCatalogServiceServer
	attrs AttributeReader
}

// NewServer создаёт gRPC-обработчик Catalog.
func NewServer(attrs AttributeReader) *Server {
	return &Server{attrs: attrs}
}

// GetEffectiveAttributes возвращает эффективный набор атрибутов категории.
// Несуществующая категория → codes.NotFound.
func (s *Server) GetEffectiveAttributes(ctx context.Context, req *catalogv1.GetEffectiveAttributesRequest) (*catalogv1.GetEffectiveAttributesResponse, error) {
	items, err := s.attrs.Effective(ctx, req.GetCategoryId())
	if errors.Is(err, domain.ErrCategoryNotFound) {
		return nil, status.Error(codes.NotFound, "category not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}

	specs := make([]*catalogv1.AttributeSpec, 0, len(items))
	for _, it := range items {
		options := make([]string, 0, len(it.Options))
		for _, o := range it.Options {
			options = append(options, o.Slug)
		}
		specs = append(specs, &catalogv1.AttributeSpec{
			Slug:     it.Slug,
			Type:     string(it.Type),
			Required: it.IsRequired,
			Options:  options,
		})
	}
	return &catalogv1.GetEffectiveAttributesResponse{Attributes: specs}, nil
}
