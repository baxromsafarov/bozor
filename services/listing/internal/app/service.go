// Package app содержит use-cases Listing-сервиса.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"bozor/pkg/shared/events"

	"bozor/services/listing/internal/domain"
)

const source = "listing"

// ErrCategoryNotFound — категория объявления не существует в Catalog.
var ErrCategoryNotFound = errors.New("категория не найдена")

// Store — персистентность объявлений (реализуется repo.Repo).
type Store interface {
	CreateWithEvent(ctx context.Context, a domain.Ad, ev events.Envelope) error
	GetByID(ctx context.Context, id string) (domain.Ad, error)
}

// CatalogValidator отдаёт эффективный набор атрибутов категории из Catalog
// (реализуется gRPC-клиентом). Возвращает ErrCategoryNotFound, если категории нет.
type CatalogValidator interface {
	EffectiveAttributes(ctx context.Context, categoryID string) ([]domain.AttrSpec, error)
}

// Service — use-cases объявлений.
type Service struct {
	store   Store
	catalog CatalogValidator
	log     *slog.Logger
}

// NewService создаёт сервис объявлений.
func NewService(store Store, catalog CatalogValidator, log *slog.Logger) *Service {
	return &Service{store: store, catalog: catalog, log: log}
}

// CreateInput — входные данные создания объявления.
type CreateInput struct {
	UserID       string
	CategoryID   string
	Title        string
	Description  string
	Price        int64
	Currency     string
	RegionID     int16
	CityID       *int64
	Lat          *float64
	Lng          *float64
	PhoneDisplay bool
	Attributes   []domain.AdAttributeValue
	Images       []domain.AdImage
}

// Create проверяет и создаёт объявление в статусе draft: валидирует поля,
// изображения и значения атрибутов против эффективного набора Catalog,
// затем сохраняет и публикует bozor.ad.created (одной транзакцией с outbox).
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Ad, error) {
	currency := in.Currency
	if currency == "" {
		currency = "UZS"
	}
	now := time.Now().UTC()
	ad := domain.Ad{
		UserID: in.UserID, CategoryID: in.CategoryID, Title: in.Title, Description: in.Description,
		Price: in.Price, Currency: currency, RegionID: in.RegionID, CityID: in.CityID,
		Lat: in.Lat, Lng: in.Lng, Status: domain.StatusDraft, PhoneDisplay: in.PhoneDisplay,
		CreatedAt: now, UpdatedAt: now, Attributes: in.Attributes, Images: in.Images,
	}
	if err := ad.ValidateCore(); err != nil {
		return domain.Ad{}, err
	}
	if err := domain.ValidateImages(ad.Images); err != nil {
		return domain.Ad{}, err
	}

	// Значения атрибутов валидируются против эффективного набора категории (Catalog).
	specs, err := s.catalog.EffectiveAttributes(ctx, in.CategoryID)
	if err != nil {
		return domain.Ad{}, err // включая ErrCategoryNotFound
	}
	if err := domain.ValidateAttributes(specs, ad.Attributes); err != nil {
		return domain.Ad{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return domain.Ad{}, fmt.Errorf("app: генерация id: %w", err)
	}
	ad.ID = id.String()

	ev, err := events.New(events.SubjectAdCreated, source, createdPayload(ad))
	if err != nil {
		return domain.Ad{}, fmt.Errorf("app: сборка события: %w", err)
	}
	if err := s.store.CreateWithEvent(ctx, ad, ev); err != nil {
		return domain.Ad{}, err
	}
	return ad, nil
}

// Get возвращает объявление по id.
func (s *Service) Get(ctx context.Context, id string) (domain.Ad, error) {
	return s.store.GetByID(ctx, id)
}

// adCreatedEvent — полезная нагрузка bozor.ad.created (без PII).
type adCreatedEvent struct {
	AdID       string `json:"ad_id"`
	UserID     string `json:"user_id"`
	CategoryID string `json:"category_id"`
	Status     string `json:"status"`
	RegionID   int16  `json:"region_id"`
	Price      int64  `json:"price"`
	Currency   string `json:"currency"`
	Title      string `json:"title"`
}

// createdPayload собирает нагрузку bozor.ad.created из объявления.
func createdPayload(a domain.Ad) adCreatedEvent {
	return adCreatedEvent{
		AdID: a.ID, UserID: a.UserID, CategoryID: a.CategoryID, Status: string(a.Status),
		RegionID: a.RegionID, Price: a.Price, Currency: a.Currency, Title: a.Title,
	}
}
