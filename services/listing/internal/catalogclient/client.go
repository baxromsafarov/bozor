// Package catalogclient — gRPC-клиент Catalog, реализующий app.CatalogValidator:
// получает эффективный набор атрибутов категории для валидации объявлений.
package catalogclient

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "bozor/pkg/shared/pb/catalog/v1"

	"bozor/services/listing/internal/app"
	"bozor/services/listing/internal/domain"
)

// Client — gRPC-клиент catalog.v1.CatalogService.
type Client struct {
	rpc catalogv1.CatalogServiceClient
}

// New создаёт клиент поверх готового соединения (см. grpcx.Dial).
func New(conn *grpc.ClientConn) *Client {
	return &Client{rpc: catalogv1.NewCatalogServiceClient(conn)}
}

// EffectiveAttributes возвращает набор спецификаций атрибутов категории.
// Несуществующая категория (codes.NotFound) → app.ErrCategoryNotFound.
func (c *Client) EffectiveAttributes(ctx context.Context, categoryID string) ([]domain.AttrSpec, error) {
	resp, err := c.rpc.GetEffectiveAttributes(ctx, &catalogv1.GetEffectiveAttributesRequest{
		CategoryId: categoryID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, app.ErrCategoryNotFound
		}
		return nil, fmt.Errorf("catalogclient: получение атрибутов категории: %w", err)
	}

	specs := make([]domain.AttrSpec, 0, len(resp.GetAttributes()))
	for _, a := range resp.GetAttributes() {
		specs = append(specs, domain.AttrSpec{
			Slug:     a.GetSlug(),
			Type:     domain.AttrType(a.GetType()),
			Required: a.GetRequired(),
			Options:  a.GetOptions(),
		})
	}
	return specs, nil
}
