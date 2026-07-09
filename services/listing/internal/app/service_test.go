package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/listing/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	created *domain.Ad
	events  []events.Envelope
}

func (f *fakeStore) CreateWithEvent(_ context.Context, a domain.Ad, ev events.Envelope) error {
	f.created = &a
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStore) GetByID(_ context.Context, _ string) (domain.Ad, error) {
	if f.created == nil {
		return domain.Ad{}, domain.ErrAdNotFound
	}
	return *f.created, nil
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
	ad, err := NewService(store, carsCatalog(), discardLogger()).Create(context.Background(), validInput())
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
	_, err := NewService(store, cat, discardLogger()).Create(context.Background(), validInput())
	assert.ErrorIs(t, err, ErrCategoryNotFound)
	assert.Nil(t, store.created, "невалидная категория — объявление не создаётся")
}

func TestCreate_UnknownAttributeRejected(t *testing.T) {
	in := validInput()
	in.Attributes = append(in.Attributes, domain.AdAttributeValue{AttributeSlug: "color", Value: "red"})
	_, err := NewService(&fakeStore{}, carsCatalog(), discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrUnknownAttribute)
}

func TestCreate_MissingRequiredAttributeRejected(t *testing.T) {
	in := validInput()
	in.Attributes = nil // brand обязателен
	_, err := NewService(&fakeStore{}, carsCatalog(), discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrMissingRequiredAttr)
}

func TestCreate_InvalidEnumValueRejected(t *testing.T) {
	in := validInput()
	in.Attributes = []domain.AdAttributeValue{{AttributeSlug: "brand", Value: "lada"}}
	_, err := NewService(&fakeStore{}, carsCatalog(), discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrInvalidAttrValue)
}

func TestCreate_CoreValidation(t *testing.T) {
	in := validInput()
	in.Title = ""
	_, err := NewService(&fakeStore{}, carsCatalog(), discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrEmptyTitle)
}

func TestCreate_TooManyImagesRejected(t *testing.T) {
	in := validInput()
	in.Images = make([]domain.AdImage, domain.MaxImages+1)
	_, err := NewService(&fakeStore{}, carsCatalog(), discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrTooManyImages)
}
