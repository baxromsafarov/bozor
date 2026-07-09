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

// ErrForbidden — действие над объявлением недоступно этому пользователю.
var ErrForbidden = errors.New("нет прав на объявление")

// Store — персистентность объявлений (реализуется repo.Repo).
type Store interface {
	CreateWithEvent(ctx context.Context, a domain.Ad, ev events.Envelope) error
	GetByID(ctx context.Context, id string) (domain.Ad, error)
	TransitionWithEvent(ctx context.Context, adID string, upd domain.StatusUpdate, ev events.Envelope) error
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
	adTTL   time.Duration
	log     *slog.Logger
}

// NewService создаёт сервис объявлений. adTTL — срок активного объявления
// (используется при продлении renew; активация из модерации задаёт срок в воркере).
func NewService(store Store, catalog CatalogValidator, adTTL time.Duration, log *slog.Logger) *Service {
	return &Service{store: store, catalog: catalog, adTTL: adTTL, log: log}
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

	ev, err := events.New(events.SubjectAdCreated, source, NewAdEvent(ad))
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

// SubmitForModeration отправляет объявление на модерацию (draft|rejected →
// pending) и публикует bozor.ad.updated. Доступно только владельцу.
func (s *Service) SubmitForModeration(ctx context.Context, adID, userID string) (domain.Ad, error) {
	return s.transition(ctx, adID, userID, func(a domain.Ad) (domain.StatusUpdate, string, error) {
		if !a.Status.CanTransitionTo(domain.StatusPending) {
			return domain.StatusUpdate{}, "", domain.ErrInvalidTransition
		}
		return domain.StatusUpdate{From: a.Status, To: domain.StatusPending}, events.SubjectAdUpdated, nil
	})
}

// MarkSold переводит активное объявление в sold и публикует bozor.ad.sold.
// Доступно только владельцу.
func (s *Service) MarkSold(ctx context.Context, adID, userID string) (domain.Ad, error) {
	return s.transition(ctx, adID, userID, func(a domain.Ad) (domain.StatusUpdate, string, error) {
		if !a.Status.CanTransitionTo(domain.StatusSold) {
			return domain.StatusUpdate{}, "", domain.ErrInvalidTransition
		}
		return domain.StatusUpdate{From: a.Status, To: domain.StatusSold}, events.SubjectAdSold, nil
	})
}

// Archive архивирует активное объявление (active → archived) и публикует
// bozor.ad.updated. Доступно только владельцу.
func (s *Service) Archive(ctx context.Context, adID, userID string) (domain.Ad, error) {
	return s.transition(ctx, adID, userID, func(a domain.Ad) (domain.StatusUpdate, string, error) {
		if !a.Status.CanTransitionTo(domain.StatusArchived) {
			return domain.StatusUpdate{}, "", domain.ErrInvalidTransition
		}
		return domain.StatusUpdate{From: a.Status, To: domain.StatusArchived}, events.SubjectAdUpdated, nil
	})
}

// Renew продлевает срок объявления: активное — сдвигает expires_at, истёкшее —
// реактивирует (expired → active) с новым сроком. Публикует bozor.ad.updated.
// Доступно только владельцу.
func (s *Service) Renew(ctx context.Context, adID, userID string) (domain.Ad, error) {
	return s.transition(ctx, adID, userID, func(a domain.Ad) (domain.StatusUpdate, string, error) {
		switch a.Status {
		case domain.StatusActive, domain.StatusExpired:
			exp := time.Now().UTC().Add(s.adTTL)
			return domain.StatusUpdate{From: a.Status, To: domain.StatusActive, ExpiresAt: &exp}, events.SubjectAdUpdated, nil
		default:
			return domain.StatusUpdate{}, "", domain.ErrInvalidTransition
		}
	})
}

// transition — общий каркас действий владельца над жизненным циклом: читает
// объявление (ErrAdNotFound), проверяет право (ErrForbidden), строит переход по
// plan (ErrInvalidTransition при недопустимости), публикует событие subject и
// применяет переход одной транзакцией. Возвращает объявление после перехода.
func (s *Service) transition(
	ctx context.Context, adID, userID string,
	plan func(domain.Ad) (domain.StatusUpdate, string, error),
) (domain.Ad, error) {
	ad, err := s.store.GetByID(ctx, adID)
	if err != nil {
		return domain.Ad{}, err
	}
	if ad.UserID != userID {
		return domain.Ad{}, ErrForbidden
	}
	upd, subject, err := plan(ad)
	if err != nil {
		return domain.Ad{}, err
	}

	// Отражаем переход в копии для полезной нагрузки события (БД меняет репозиторий).
	ad.Status = upd.To
	if upd.PublishedAt != nil {
		ad.PublishedAt = upd.PublishedAt
	}
	if upd.ExpiresAt != nil {
		ad.ExpiresAt = upd.ExpiresAt
	}

	ev, err := events.New(subject, source, NewAdEvent(ad))
	if err != nil {
		return domain.Ad{}, fmt.Errorf("app: сборка события: %w", err)
	}
	if err := s.store.TransitionWithEvent(ctx, adID, upd, ev); err != nil {
		return domain.Ad{}, err
	}
	return ad, nil
}

// AdEvent — полезная нагрузка событий жизненного цикла объявления
// (bozor.ad.created|updated|sold|expired), без PII. published_at/expires_at
// присутствуют, когда заданы (для индексации в Search).
type AdEvent struct {
	AdID        string     `json:"ad_id"`
	UserID      string     `json:"user_id"`
	CategoryID  string     `json:"category_id"`
	Status      string     `json:"status"`
	RegionID    int16      `json:"region_id"`
	Price       int64      `json:"price"`
	Currency    string     `json:"currency"`
	Title       string     `json:"title"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// NewAdEvent собирает нагрузку события жизненного цикла из объявления.
func NewAdEvent(a domain.Ad) AdEvent {
	return AdEvent{
		AdID: a.ID, UserID: a.UserID, CategoryID: a.CategoryID, Status: string(a.Status),
		RegionID: a.RegionID, Price: a.Price, Currency: a.Currency, Title: a.Title,
		PublishedAt: a.PublishedAt, ExpiresAt: a.ExpiresAt,
	}
}
