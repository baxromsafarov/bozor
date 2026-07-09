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

	"bozor/services/user-profile/internal/domain"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func timeZero() time.Time { return time.Time{} }

// fakeStore — in-memory реализация Store.
type fakeStore struct {
	profiles map[string]domain.Profile
	ratings  map[string]domain.Rating
	prefs    map[string][]domain.NotificationPref
	events   []events.Envelope
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		profiles: map[string]domain.Profile{},
		ratings:  map[string]domain.Rating{},
		prefs:    map[string][]domain.NotificationPref{},
	}
}

func (f *fakeStore) EnsureProfile(_ context.Context, p domain.Profile) error {
	if _, ok := f.profiles[p.UserID]; !ok {
		f.profiles[p.UserID] = p
	}
	return nil
}

func (f *fakeStore) GetProfile(_ context.Context, userID string) (domain.Profile, error) {
	p, ok := f.profiles[userID]
	if !ok {
		return domain.Profile{}, domain.ErrProfileNotFound
	}
	return p, nil
}

func (f *fakeStore) UpdateProfileWithEvent(_ context.Context, p domain.Profile, ev events.Envelope) error {
	if _, ok := f.profiles[p.UserID]; !ok {
		return domain.ErrProfileNotFound
	}
	f.profiles[p.UserID] = p
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStore) GetRating(_ context.Context, userID string) (domain.Rating, error) {
	return f.ratings[userID], nil
}

func (f *fakeStore) GetNotificationPrefs(_ context.Context, userID string) ([]domain.NotificationPref, error) {
	return f.prefs[userID], nil
}

func (f *fakeStore) ReplaceNotificationPrefs(_ context.Context, userID string, prefs []domain.NotificationPref) error {
	f.prefs[userID] = prefs
	return nil
}

func strptr(s string) *string { return &s }
func i64ptr(v int64) *int64   { return &v }

func TestService_Me_LazyCreatesDefault(t *testing.T) {
	store := newFakeStore()
	p, err := NewService(store, discardLogger()).Me(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, "u1", p.UserID)
	assert.Equal(t, domain.UserTypeIndividual, p.UserType, "лениво создан профиль по умолчанию")
}

func TestService_UpdateMe_AppliesValidatesEmits(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, discardLogger())

	in := UpdateInput{
		DisplayName:  strptr("Азиз"),
		About:        strptr("Продаю технику"),
		UserType:     strptr("business"),
		BusinessName: strptr("TechShop"),
		CityID:       i64ptr(14),
	}
	p, err := svc.UpdateMe(context.Background(), "u1", in)
	require.NoError(t, err)
	assert.Equal(t, "Азиз", p.DisplayName)
	assert.Equal(t, domain.UserTypeBusiness, p.UserType)
	require.NotNil(t, p.CityID)
	assert.Equal(t, int64(14), *p.CityID)
	require.Len(t, store.events, 1, "опубликовано bozor.user.updated")
	assert.Equal(t, events.SubjectUserUpdated, store.events[0].Type)
}

func TestService_UpdateMe_ValidationRejected(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, discardLogger())
	// business без названия — отклонено, событие не публикуется.
	_, err := svc.UpdateMe(context.Background(), "u1", UpdateInput{UserType: strptr("business")})
	require.ErrorIs(t, err, domain.ErrBusinessNameRequired)
	assert.Empty(t, store.events)
}

func TestService_UpdateMe_InvalidAvatar(t *testing.T) {
	svc := NewService(newFakeStore(), discardLogger())
	_, err := svc.UpdateMe(context.Background(), "u1", UpdateInput{AvatarMediaID: strptr("not-a-uuid")})
	require.ErrorIs(t, err, domain.ErrInvalidAvatar)
}

func TestApplyUpdate_ClearsNullable(t *testing.T) {
	base := domain.NewDefaultProfile("u1", "ru", timeZero())
	base.AvatarMediaID = strptr("11111111-1111-4111-8111-111111111111")
	base.CityID = i64ptr(5)

	// Пустая строка аватара и неположительный город очищают значения.
	out, err := applyUpdate(base, UpdateInput{AvatarMediaID: strptr(""), CityID: i64ptr(0)})
	require.NoError(t, err)
	assert.Nil(t, out.AvatarMediaID, "пустой аватар очищен")
	assert.Nil(t, out.CityID, "неположительный город очищен")
}

func TestService_PublicProfile_WithRating(t *testing.T) {
	store := newFakeStore()
	store.profiles["seller"] = domain.NewDefaultProfile("seller", "ru", timeZero())
	store.ratings["seller"] = domain.Rating{AvgRating: 4.5, ReviewsCount: 12}

	pp, err := NewService(store, discardLogger()).PublicProfile(context.Background(), "seller")
	require.NoError(t, err)
	assert.Equal(t, "seller", pp.Profile.UserID)
	assert.Equal(t, 4.5, pp.Rating.AvgRating)
	assert.Equal(t, 12, pp.Rating.ReviewsCount)
}

func TestService_PublicProfile_NotFound(t *testing.T) {
	_, err := NewService(newFakeStore(), discardLogger()).PublicProfile(context.Background(), "ghost")
	require.ErrorIs(t, err, domain.ErrProfileNotFound)
}

func TestService_NotificationPrefs_Effective(t *testing.T) {
	store := newFakeStore()
	store.prefs["u1"] = []domain.NotificationPref{{Channel: domain.ChannelTelegram, EventType: domain.NotifyReview, Enabled: false}}
	prefs, err := NewService(store, discardLogger()).NotificationPrefs(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, prefs, 5)
	for _, p := range prefs {
		if p.EventType == domain.NotifyReview {
			assert.False(t, p.Enabled)
		}
	}
}

func TestService_SetNotificationPrefs_ValidatesAndDedups(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, discardLogger())

	// Дубль пары (канал,тип): последняя запись побеждает.
	_, err := svc.SetNotificationPrefs(context.Background(), "u1", []domain.NotificationPref{
		{Channel: domain.ChannelTelegram, EventType: domain.NotifyAdStatus, Enabled: true},
		{Channel: domain.ChannelTelegram, EventType: domain.NotifyAdStatus, Enabled: false},
	})
	require.NoError(t, err)
	require.Len(t, store.prefs["u1"], 1, "дубль схлопнут")
	assert.False(t, store.prefs["u1"][0].Enabled, "последняя запись победила")
	_, ensured := store.profiles["u1"]
	assert.True(t, ensured, "профиль создан лениво под FK")
}

func TestService_SetNotificationPrefs_InvalidRejected(t *testing.T) {
	svc := NewService(newFakeStore(), discardLogger())
	_, err := svc.SetNotificationPrefs(context.Background(), "u1", []domain.NotificationPref{
		{Channel: "email", EventType: domain.NotifyAdStatus, Enabled: true},
	})
	require.ErrorIs(t, err, domain.ErrInvalidNotificationPref)
}
