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
	UpdateWithEvent(ctx context.Context, a domain.Ad, ev events.Envelope) error
	DeleteWithEvent(ctx context.Context, adID string, ev events.Envelope) error
	BumpWithEvent(ctx context.Context, adID string, bumpedAt time.Time, ev events.Envelope) (bool, error)
	ListActive(ctx context.Context, f domain.FeedFilter) ([]domain.Ad, error)
	ListByUser(ctx context.Context, userID, status string, limit, offset int) ([]domain.Ad, error)
	ListActiveFull(ctx context.Context, after string, limit int) ([]domain.Ad, error)
}

// Ограничения пагинации ленты и списка «мои объявления».
const (
	defaultPageLimit = 20
	maxPageLimit     = 100
)

// CatalogValidator отдаёт эффективный набор атрибутов категории из Catalog
// (реализуется gRPC-клиентом). Возвращает ErrCategoryNotFound, если категории нет.
type CatalogValidator interface {
	EffectiveAttributes(ctx context.Context, categoryID string) ([]domain.AttrSpec, error)
}

// ViewCounter буферизует просмотры объявлений (реализуется views.Counter).
// Инкремент/чтение — best-effort: недоступность Redis не должна ломать чтение
// объявления. nil-реализация отключает счётчик.
type ViewCounter interface {
	Incr(ctx context.Context, adID string) error
	Buffered(ctx context.Context, adID string) (int64, error)
}

// Service — use-cases объявлений.
type Service struct {
	store   Store
	catalog CatalogValidator
	views   ViewCounter
	adTTL   time.Duration
	log     *slog.Logger
}

// NewService создаёт сервис объявлений. adTTL — срок активного объявления
// (используется при продлении renew; активация из модерации задаёт срок в воркере).
// views может быть nil (счётчик просмотров отключён).
func NewService(store Store, catalog CatalogValidator, views ViewCounter, adTTL time.Duration, log *slog.Logger) *Service {
	return &Service{store: store, catalog: catalog, views: views, adTTL: adTTL, log: log}
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

// Get возвращает объявление по id и учитывает его просмотр: инкрементирует
// буфер в Redis и добавляет ещё не слитые просмотры к персистентному счётчику
// (near-real-time). Счётчик — best-effort: сбой Redis не мешает чтению.
func (s *Service) Get(ctx context.Context, id string) (domain.Ad, error) {
	ad, err := s.store.GetByID(ctx, id)
	if err != nil {
		return domain.Ad{}, err
	}
	s.recordView(ctx, &ad)
	return ad, nil
}

// recordView учитывает просмотр объявления (best-effort): +1 в буфер и
// добавление буферизованных просмотров к персистентному значению.
func (s *Service) recordView(ctx context.Context, ad *domain.Ad) {
	if s.views == nil {
		return
	}
	if err := s.views.Incr(ctx, ad.ID); err != nil {
		s.log.WarnContext(ctx, "счётчик просмотров: инкремент", slog.String("error", err.Error()))
	}
	buffered, err := s.views.Buffered(ctx, ad.ID)
	if err != nil {
		s.log.WarnContext(ctx, "счётчик просмотров: чтение буфера", slog.String("error", err.Error()))
		return
	}
	ad.ViewsCount += buffered
}

// UpdateInput — частичное изменение объявления (nil-поля не меняются).
type UpdateInput struct {
	Title        *string
	Description  *string
	Price        *int64
	Currency     *string
	CategoryID   *string
	RegionID     *int16
	CityID       *int64
	Lat          *float64
	Lng          *float64
	PhoneDisplay *bool
	Attributes   *[]domain.AdAttributeValue
	Images       *[]domain.AdImage
}

// Update изменяет объявление владельца: применяет заданные поля, ре-валидирует
// значения атрибутов против (возможно новой) категории Catalog и, если правка
// затронула ключевые поля активного объявления (заголовок, описание, цена,
// категория, атрибуты, изображения), отправляет его на повторную модерацию
// (active → pending). Публикует bozor.ad.updated. Терминальные/заблокированные
// править нельзя (ErrNotEditable).
func (s *Service) Update(ctx context.Context, adID, userID string, in UpdateInput) (domain.Ad, error) {
	ad, err := s.store.GetByID(ctx, adID)
	if err != nil {
		return domain.Ad{}, err
	}
	if ad.UserID != userID {
		return domain.Ad{}, ErrForbidden
	}
	if !ad.Status.Editable() {
		return domain.Ad{}, domain.ErrNotEditable
	}

	keyChanged := applyUpdate(&ad, in)

	if err := ad.ValidateCore(); err != nil {
		return domain.Ad{}, err
	}
	if err := domain.ValidateImages(ad.Images); err != nil {
		return domain.Ad{}, err
	}
	specs, err := s.catalog.EffectiveAttributes(ctx, ad.CategoryID)
	if err != nil {
		return domain.Ad{}, err // включая ErrCategoryNotFound
	}
	if err := domain.ValidateAttributes(specs, ad.Attributes); err != nil {
		return domain.Ad{}, err
	}

	// Правка ключевых полей активного объявления → повторная модерация.
	if keyChanged && ad.Status == domain.StatusActive {
		ad.Status = domain.StatusPending
	}
	ad.UpdatedAt = time.Now().UTC()

	ev, err := events.New(events.SubjectAdUpdated, source, NewAdEvent(ad))
	if err != nil {
		return domain.Ad{}, fmt.Errorf("app: сборка события: %w", err)
	}
	if err := s.store.UpdateWithEvent(ctx, ad, ev); err != nil {
		return domain.Ad{}, err
	}
	return ad, nil
}

// applyUpdate применяет заданные поля к объявлению и сообщает, изменились ли
// ключевые поля (влияющие на повторную модерацию): заголовок, описание, цена,
// категория, атрибуты, изображения.
func applyUpdate(ad *domain.Ad, in UpdateInput) bool {
	keyChanged := false
	if in.Title != nil && *in.Title != ad.Title {
		ad.Title, keyChanged = *in.Title, true
	}
	if in.Description != nil && *in.Description != ad.Description {
		ad.Description, keyChanged = *in.Description, true
	}
	if in.Price != nil && *in.Price != ad.Price {
		ad.Price, keyChanged = *in.Price, true
	}
	if in.CategoryID != nil && *in.CategoryID != ad.CategoryID {
		ad.CategoryID, keyChanged = *in.CategoryID, true
	}
	if in.Attributes != nil {
		ad.Attributes, keyChanged = *in.Attributes, true
	}
	if in.Images != nil {
		ad.Images, keyChanged = *in.Images, true
	}
	// Неключевые поля — без повторной модерации.
	if in.Currency != nil {
		ad.Currency = *in.Currency
	}
	if in.RegionID != nil {
		ad.RegionID = *in.RegionID
	}
	if in.CityID != nil {
		ad.CityID = in.CityID
	}
	if in.Lat != nil {
		ad.Lat = in.Lat
	}
	if in.Lng != nil {
		ad.Lng = in.Lng
	}
	if in.PhoneDisplay != nil {
		ad.PhoneDisplay = *in.PhoneDisplay
	}
	return keyChanged
}

// Delete удаляет объявление владельца и публикует bozor.ad.deleted (Search/Media
// снимают индекс/освобождают медиа). Доступно только владельцу.
func (s *Service) Delete(ctx context.Context, adID, userID string) error {
	ad, err := s.store.GetByID(ctx, adID)
	if err != nil {
		return err
	}
	if ad.UserID != userID {
		return ErrForbidden
	}
	ev, err := events.New(events.SubjectAdDeleted, source, NewAdEvent(ad))
	if err != nil {
		return fmt.Errorf("app: сборка события: %w", err)
	}
	return s.store.DeleteWithEvent(ctx, adID, ev)
}

// Feed возвращает ленту активных объявлений с пагинацией и сортировкой.
func (s *Service) Feed(ctx context.Context, f domain.FeedFilter) ([]domain.Ad, error) {
	f.Limit, f.Offset = clampPage(f.Limit, f.Offset)
	return s.store.ListActive(ctx, f)
}

// MyAds возвращает объявления пользователя (все или заданного статуса) с пагинацией.
func (s *Service) MyAds(ctx context.Context, userID, status string, limit, offset int) ([]domain.Ad, error) {
	limit, offset = clampPage(limit, offset)
	return s.store.ListByUser(ctx, userID, status, limit, offset)
}

// ExportByID возвращает полное объявление по id БЕЗ учёта просмотра — для
// внутренней синхронизации read-модели Search (Stage 4.2).
func (s *Service) ExportByID(ctx context.Context, id string) (domain.Ad, error) {
	return s.store.GetByID(ctx, id)
}

// ExportActive возвращает активные объявления с полными данными (keyset по id) —
// источник для полной переиндексации Search.
func (s *Service) ExportActive(ctx context.Context, after string, limit int) ([]domain.Ad, error) {
	limit, _ = clampPage(limit, 0)
	return s.store.ListActiveFull(ctx, after, limit)
}

// clampPage приводит лимит/смещение к безопасным границам.
func clampPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
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

// AdBumpEvent — нагрузка bozor.ad.bumped: id объявления и момент поднятия.
// Индексатор Search использует только ad_id (читает актуальный bumped_at из
// Listing), bumped_at передаётся для наблюдаемости и потребителей уведомлений.
type AdBumpEvent struct {
	AdID     string    `json:"ad_id"`
	BumpedAt time.Time `json:"bumped_at"`
}

// Bump поднимает объявление в ленте: выставляет bumped_at=now у активного
// объявления и публикует bozor.ad.bumped одной транзакцией. Возвращает false,
// если объявления нет или оно не активно (поднимать нечего — идемпотентный
// пропуск). Вызывается внутренним эндпоинтом по триггеру воркера авто-поднятий
// Payments (Stage 8.5); Search переиндексирует по событию.
func (s *Service) Bump(ctx context.Context, adID string) (bool, error) {
	bumpedAt := time.Now().UTC()
	ev, err := events.New(events.SubjectAdBumped, source, AdBumpEvent{AdID: adID, BumpedAt: bumpedAt})
	if err != nil {
		return false, err
	}
	return s.store.BumpWithEvent(ctx, adID, bumpedAt, ev)
}
