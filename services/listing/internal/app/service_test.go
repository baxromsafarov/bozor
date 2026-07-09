package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/listing/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	created   *domain.Ad
	events    []events.Envelope
	getResult *domain.Ad // если задан — возвращается GetByID (для действий жизненного цикла)
	getErr    error
	lastUpd   domain.StatusUpdate // последний переход, переданный в TransitionWithEvent
	transErr  error
}

func (f *fakeStore) CreateWithEvent(_ context.Context, a domain.Ad, ev events.Envelope) error {
	f.created = &a
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStore) GetByID(_ context.Context, _ string) (domain.Ad, error) {
	switch {
	case f.getErr != nil:
		return domain.Ad{}, f.getErr
	case f.getResult != nil:
		return *f.getResult, nil
	case f.created != nil:
		return *f.created, nil
	}
	return domain.Ad{}, domain.ErrAdNotFound
}

func (f *fakeStore) TransitionWithEvent(_ context.Context, _ string, upd domain.StatusUpdate, ev events.Envelope) error {
	f.lastUpd = upd
	if f.transErr != nil {
		return f.transErr
	}
	f.events = append(f.events, ev)
	return nil
}

type fakeCatalog struct {
	specs []domain.AttrSpec
	err   error
}

func (f *fakeCatalog) EffectiveAttributes(_ context.Context, _ string) ([]domain.AttrSpec, error) {
	return f.specs, f.err
}

func validInput() CreateInput {
	return CreateInput{
		UserID: "user-1", CategoryID: "cat-cars", Title: "BMW X5", Price: 500000000,
		RegionID: 1, PhoneDisplay: true,
		Attributes: []domain.AdAttributeValue{{AttributeSlug: "brand", Value: "bmw"}},
	}
}

func carsCatalog() *fakeCatalog {
	return &fakeCatalog{specs: []domain.AttrSpec{
		{Slug: "brand", Type: domain.AttrEnum, Required: true, Options: []string{"bmw", "audi"}},
		{Slug: "year", Type: domain.AttrInt},
	}}
}

func TestCreate_HappyPath(t *testing.T) {
	store := &fakeStore{}
	ad, err := NewService(store, carsCatalog(), 720*time.Hour, discardLogger()).Create(context.Background(), validInput())
	require.NoError(t, err)
	assert.NotEmpty(t, ad.ID)
	assert.Equal(t, domain.StatusDraft, ad.Status)
	assert.Equal(t, "UZS", ad.Currency, "валюта по умолчанию")
	require.NotNil(t, store.created)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectAdCreated, store.events[0].Type)
}

func TestCreate_CategoryNotFound(t *testing.T) {
	store := &fakeStore{}
	cat := &fakeCatalog{err: ErrCategoryNotFound}
	_, err := NewService(store, cat, 720*time.Hour, discardLogger()).Create(context.Background(), validInput())
	assert.ErrorIs(t, err, ErrCategoryNotFound)
	assert.Nil(t, store.created, "невалидная категория — объявление не создаётся")
}

func TestCreate_UnknownAttributeRejected(t *testing.T) {
	in := validInput()
	in.Attributes = append(in.Attributes, domain.AdAttributeValue{AttributeSlug: "color", Value: "red"})
	_, err := NewService(&fakeStore{}, carsCatalog(), 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrUnknownAttribute)
}

func TestCreate_MissingRequiredAttributeRejected(t *testing.T) {
	in := validInput()
	in.Attributes = nil // brand обязателен
	_, err := NewService(&fakeStore{}, carsCatalog(), 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrMissingRequiredAttr)
}

func TestCreate_InvalidEnumValueRejected(t *testing.T) {
	in := validInput()
	in.Attributes = []domain.AdAttributeValue{{AttributeSlug: "brand", Value: "lada"}}
	_, err := NewService(&fakeStore{}, carsCatalog(), 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrInvalidAttrValue)
}

func TestCreate_CoreValidation(t *testing.T) {
	in := validInput()
	in.Title = ""
	_, err := NewService(&fakeStore{}, carsCatalog(), 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrEmptyTitle)
}

func TestCreate_TooManyImagesRejected(t *testing.T) {
	in := validInput()
	in.Images = make([]domain.AdImage, domain.MaxImages+1)
	_, err := NewService(&fakeStore{}, carsCatalog(), 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrTooManyImages)
}

// lifecycleSvc — сервис для действий жизненного цикла (каталог не задействован).
func lifecycleSvc(store Store) *Service {
	return NewService(store, nil, 720*time.Hour, discardLogger())
}

func adWith(status domain.Status) *domain.Ad {
	return &domain.Ad{
		ID: "ad-1", UserID: "user-1", CategoryID: "cat", Title: "BMW X5",
		Price: 100, Currency: "UZS", RegionID: 1, Status: status,
	}
}

func TestSubmitForModeration_HappyPath(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusDraft)}
	ad, err := lifecycleSvc(store).SubmitForModeration(context.Background(), "ad-1", "user-1")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPending, ad.Status)
	assert.Equal(t, domain.StatusDraft, store.lastUpd.From)
	assert.Equal(t, domain.StatusPending, store.lastUpd.To)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectAdUpdated, store.events[0].Type)
}

func TestSubmitForModeration_FromRejected(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusRejected)}
	ad, err := lifecycleSvc(store).SubmitForModeration(context.Background(), "ad-1", "user-1")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPending, ad.Status)
}

func TestLifecycle_WrongOwnerForbidden(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusDraft)}
	_, err := lifecycleSvc(store).SubmitForModeration(context.Background(), "ad-1", "intruder")
	assert.ErrorIs(t, err, ErrForbidden)
	assert.Nil(t, store.events, "переход не выполняется без прав")
}

func TestLifecycle_NotFound(t *testing.T) {
	store := &fakeStore{getErr: domain.ErrAdNotFound}
	_, err := lifecycleSvc(store).SubmitForModeration(context.Background(), "nope", "user-1")
	assert.ErrorIs(t, err, domain.ErrAdNotFound)
}

func TestSubmitForModeration_InvalidFromSold(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusSold)}
	_, err := lifecycleSvc(store).SubmitForModeration(context.Background(), "ad-1", "user-1")
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
	assert.Nil(t, store.events)
}

func TestMarkSold_HappyPath(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusActive)}
	ad, err := lifecycleSvc(store).MarkSold(context.Background(), "ad-1", "user-1")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusSold, ad.Status)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectAdSold, store.events[0].Type)
}

func TestMarkSold_NotActiveRejected(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusDraft)}
	_, err := lifecycleSvc(store).MarkSold(context.Background(), "ad-1", "user-1")
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
}

func TestRenew_ActiveExtendsExpiry(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusActive)}
	ad, err := lifecycleSvc(store).Renew(context.Background(), "ad-1", "user-1")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, ad.Status)
	require.NotNil(t, ad.ExpiresAt, "renew задаёт новый срок")
	assert.Equal(t, domain.StatusActive, store.lastUpd.To)
	require.NotNil(t, store.lastUpd.ExpiresAt)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectAdUpdated, store.events[0].Type)
}

func TestRenew_ExpiredReactivates(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusExpired)}
	ad, err := lifecycleSvc(store).Renew(context.Background(), "ad-1", "user-1")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, ad.Status)
	assert.Equal(t, domain.StatusExpired, store.lastUpd.From)
}

func TestRenew_DraftRejected(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusDraft)}
	_, err := lifecycleSvc(store).Renew(context.Background(), "ad-1", "user-1")
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
}

func TestArchive_HappyPath(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusActive)}
	ad, err := lifecycleSvc(store).Archive(context.Background(), "ad-1", "user-1")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusArchived, ad.Status)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectAdUpdated, store.events[0].Type)
}
