package app

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/listing/internal/domain"
)

func ptr[T any](v T) *T { return &v }

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeStore struct {
	created     *domain.Ad
	events      []events.Envelope
	getResult   *domain.Ad // если задан — возвращается GetByID (для действий жизненного цикла)
	getErr      error
	lastUpd     domain.StatusUpdate // последний переход, переданный в TransitionWithEvent
	transErr    error
	updated     *domain.Ad        // последнее объявление, переданное в UpdateWithEvent
	deletedID   string            // id из DeleteWithEvent
	list        []domain.Ad       // результат ListActive/ListByUser
	feedFilter  domain.FeedFilter // фильтр из ListActive
	byUser      [4]string         // {userID, status, limit, offset} из ListByUser
	exportAfter string            // after из ListActiveFull
	exportLimit int               // limit из ListActiveFull
	bumpedID    string            // id из BumpWithEvent
	bumpResult  bool              // что вернёт BumpWithEvent (по умолчанию false)
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

func (f *fakeStore) UpdateWithEvent(_ context.Context, a domain.Ad, ev events.Envelope) error {
	f.updated = &a
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStore) DeleteWithEvent(_ context.Context, adID string, ev events.Envelope) error {
	f.deletedID = adID
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStore) ListActive(_ context.Context, filter domain.FeedFilter) ([]domain.Ad, error) {
	f.feedFilter = filter
	return f.list, nil
}

func (f *fakeStore) ListByUser(_ context.Context, userID, status string, limit, offset int) ([]domain.Ad, error) {
	f.byUser = [4]string{userID, status, itoa(limit), itoa(offset)}
	return f.list, nil
}

func (f *fakeStore) ListActiveFull(_ context.Context, after string, limit int) ([]domain.Ad, error) {
	f.exportAfter, f.exportLimit = after, limit
	return f.list, nil
}

func (f *fakeStore) BumpWithEvent(_ context.Context, adID string, _ time.Time, ev events.Envelope) (bool, error) {
	f.bumpedID = adID
	if !f.bumpResult {
		return false, nil
	}
	f.events = append(f.events, ev)
	return true, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

type fakeCatalog struct {
	specs []domain.AttrSpec
	err   error
}

func (f *fakeCatalog) EffectiveAttributes(_ context.Context, _ string) ([]domain.AttrSpec, error) {
	return f.specs, f.err
}

type fakeViews struct {
	incred   []string
	buffered int64
	incrErr  error
}

func (f *fakeViews) Incr(_ context.Context, adID string) error {
	f.incred = append(f.incred, adID)
	return f.incrErr
}

func (f *fakeViews) Buffered(_ context.Context, _ string) (int64, error) {
	return f.buffered, nil
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
	ad, err := NewService(store, carsCatalog(), nil, 720*time.Hour, discardLogger()).Create(context.Background(), validInput())
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
	_, err := NewService(store, cat, nil, 720*time.Hour, discardLogger()).Create(context.Background(), validInput())
	assert.ErrorIs(t, err, ErrCategoryNotFound)
	assert.Nil(t, store.created, "невалидная категория — объявление не создаётся")
}

func TestCreate_UnknownAttributeRejected(t *testing.T) {
	in := validInput()
	in.Attributes = append(in.Attributes, domain.AdAttributeValue{AttributeSlug: "color", Value: "red"})
	_, err := NewService(&fakeStore{}, carsCatalog(), nil, 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrUnknownAttribute)
}

func TestCreate_MissingRequiredAttributeRejected(t *testing.T) {
	in := validInput()
	in.Attributes = nil // brand обязателен
	_, err := NewService(&fakeStore{}, carsCatalog(), nil, 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrMissingRequiredAttr)
}

func TestCreate_InvalidEnumValueRejected(t *testing.T) {
	in := validInput()
	in.Attributes = []domain.AdAttributeValue{{AttributeSlug: "brand", Value: "lada"}}
	_, err := NewService(&fakeStore{}, carsCatalog(), nil, 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrInvalidAttrValue)
}

func TestCreate_CoreValidation(t *testing.T) {
	in := validInput()
	in.Title = ""
	_, err := NewService(&fakeStore{}, carsCatalog(), nil, 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrEmptyTitle)
}

func TestCreate_TooManyImagesRejected(t *testing.T) {
	in := validInput()
	in.Images = make([]domain.AdImage, domain.MaxImages+1)
	_, err := NewService(&fakeStore{}, carsCatalog(), nil, 720*time.Hour, discardLogger()).Create(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrTooManyImages)
}

// lifecycleSvc — сервис для действий жизненного цикла (каталог не задействован).
func lifecycleSvc(store Store) *Service {
	return NewService(store, nil, nil, 720*time.Hour, discardLogger())
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

// updatableAd — объявление владельца в заданном статусе с валидными атрибутами.
func updatableAd(status domain.Status) *domain.Ad {
	return &domain.Ad{
		ID: "ad-1", UserID: "user-1", CategoryID: "cat", Title: "BMW X5", Price: 100,
		Currency: "UZS", RegionID: 1, Status: status,
		Attributes: []domain.AdAttributeValue{{AttributeSlug: "brand", Value: "bmw"}},
	}
}

func updateSvc(store Store) *Service {
	return NewService(store, carsCatalog(), nil, 720*time.Hour, discardLogger())
}

func TestUpdate_ActiveKeyFieldTriggersRemoderation(t *testing.T) {
	store := &fakeStore{getResult: updatableAd(domain.StatusActive)}
	ad, err := updateSvc(store).Update(context.Background(), "ad-1", "user-1",
		UpdateInput{Price: ptr(int64(200))})
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPending, ad.Status, "правка цены активного → повторная модерация")
	assert.Equal(t, int64(200), ad.Price)
	require.NotNil(t, store.updated)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectAdUpdated, store.events[0].Type)
}

func TestUpdate_ActiveNonKeyFieldKeepsActive(t *testing.T) {
	store := &fakeStore{getResult: updatableAd(domain.StatusActive)}
	ad, err := updateSvc(store).Update(context.Background(), "ad-1", "user-1",
		UpdateInput{PhoneDisplay: ptr(false)})
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, ad.Status, "неключевое поле не шлёт на модерацию")
	assert.False(t, ad.PhoneDisplay)
}

func TestUpdate_DraftStaysDraft(t *testing.T) {
	store := &fakeStore{getResult: updatableAd(domain.StatusDraft)}
	ad, err := updateSvc(store).Update(context.Background(), "ad-1", "user-1",
		UpdateInput{Title: ptr("BMW X6")})
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDraft, ad.Status)
	assert.Equal(t, "BMW X6", ad.Title)
}

func TestUpdate_WrongOwnerForbidden(t *testing.T) {
	store := &fakeStore{getResult: updatableAd(domain.StatusActive)}
	_, err := updateSvc(store).Update(context.Background(), "ad-1", "intruder", UpdateInput{Price: ptr(int64(1))})
	assert.ErrorIs(t, err, ErrForbidden)
}

func TestUpdate_TerminalNotEditable(t *testing.T) {
	store := &fakeStore{getResult: updatableAd(domain.StatusSold)}
	_, err := updateSvc(store).Update(context.Background(), "ad-1", "user-1", UpdateInput{Title: ptr("X")})
	assert.ErrorIs(t, err, domain.ErrNotEditable)
}

func TestUpdate_RevalidatesAttributes(t *testing.T) {
	store := &fakeStore{getResult: updatableAd(domain.StatusDraft)}
	bad := []domain.AdAttributeValue{{AttributeSlug: "brand", Value: "lada"}} // вне enum
	_, err := updateSvc(store).Update(context.Background(), "ad-1", "user-1", UpdateInput{Attributes: &bad})
	assert.ErrorIs(t, err, domain.ErrInvalidAttrValue)
}

func TestDelete_HappyPath(t *testing.T) {
	store := &fakeStore{getResult: updatableAd(domain.StatusActive)}
	err := updateSvc(store).Delete(context.Background(), "ad-1", "user-1")
	require.NoError(t, err)
	assert.Equal(t, "ad-1", store.deletedID)
	require.Len(t, store.events, 1)
	assert.Equal(t, events.SubjectAdDeleted, store.events[0].Type)
}

func TestDelete_WrongOwnerForbidden(t *testing.T) {
	store := &fakeStore{getResult: updatableAd(domain.StatusActive)}
	err := updateSvc(store).Delete(context.Background(), "ad-1", "intruder")
	assert.ErrorIs(t, err, ErrForbidden)
	assert.Empty(t, store.deletedID)
}

func TestFeed_ClampsLimit(t *testing.T) {
	store := &fakeStore{list: []domain.Ad{{ID: "a"}}}
	ads, err := updateSvc(store).Feed(context.Background(), domain.FeedFilter{Limit: 1000, CategoryID: "cat"})
	require.NoError(t, err)
	require.Len(t, ads, 1)
	assert.Equal(t, maxPageLimit, store.feedFilter.Limit, "лимит ограничен максимумом")
	assert.Equal(t, "cat", store.feedFilter.CategoryID)
}

func TestFeed_DefaultLimit(t *testing.T) {
	store := &fakeStore{}
	_, err := updateSvc(store).Feed(context.Background(), domain.FeedFilter{})
	require.NoError(t, err)
	assert.Equal(t, defaultPageLimit, store.feedFilter.Limit)
}

func TestMyAds_PassesParams(t *testing.T) {
	store := &fakeStore{}
	_, err := updateSvc(store).MyAds(context.Background(), "user-1", "active", 0, 5)
	require.NoError(t, err)
	assert.Equal(t, [4]string{"user-1", "active", strconv.Itoa(defaultPageLimit), "5"}, store.byUser)
}

func TestGet_RecordsViewAndAugments(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusActive)} // персистентно views_count=0
	vc := &fakeViews{buffered: 7}
	ad, err := NewService(store, nil, vc, 720*time.Hour, discardLogger()).Get(context.Background(), "ad-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"ad-1"}, vc.incred, "просмотр учтён в буфере")
	assert.Equal(t, int64(7), ad.ViewsCount, "к персистентному счётчику добавлены буферизованные просмотры")
}

func TestGet_ViewCounterFailureIsBestEffort(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusActive)}
	vc := &fakeViews{incrErr: assertErr}
	ad, err := NewService(store, nil, vc, 720*time.Hour, discardLogger()).Get(context.Background(), "ad-1")
	require.NoError(t, err, "сбой счётчика не ломает чтение")
	assert.Equal(t, "ad-1", ad.ID)
}

func TestGet_NilViewsOK(t *testing.T) {
	store := &fakeStore{getResult: adWith(domain.StatusActive)}
	ad, err := NewService(store, nil, nil, 720*time.Hour, discardLogger()).Get(context.Background(), "ad-1")
	require.NoError(t, err)
	assert.Equal(t, "ad-1", ad.ID)
	assert.Equal(t, int64(0), ad.ViewsCount)
}

var assertErr = errStub("boom")

type errStub string

func (e errStub) Error() string { return string(e) }
