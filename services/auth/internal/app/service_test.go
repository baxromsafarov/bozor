package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/apperr"
	"bozor/pkg/shared/events"

	"bozor/services/auth/internal/domain"
)

// fakeStore фиксирует вызов и управляет результатом.
type fakeStore struct {
	called  bool
	gotUser domain.User
	created bool
	err     error
}

func (f *fakeStore) UpsertUserWithEvent(_ context.Context, u domain.User, _ events.Envelope) (string, bool, error) {
	f.called = true
	f.gotUser = u
	return u.ID, f.created, f.err
}

func TestRegisterContact_RejectsForwardedContact(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store)

	// contact.user_id != from.id — переслан чужой контакт.
	_, err := svc.RegisterContact(context.Background(), Contact{
		FromID: 100, ContactUserID: 200, PhoneNumber: "+998901234567",
	})
	require.Error(t, err)
	assert.Equal(t, apperr.KindForbidden, apperr.KindOf(err))
	assert.False(t, store.called, "хранилище не вызывается при чужом контакте")
}

func TestRegisterContact_RejectsMissingContactUserID(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store)

	_, err := svc.RegisterContact(context.Background(), Contact{
		FromID: 100, ContactUserID: 0, PhoneNumber: "+998901234567",
	})
	require.Error(t, err)
	assert.Equal(t, apperr.KindForbidden, apperr.KindOf(err))
	assert.False(t, store.called)
}

func TestRegisterContact_RejectsInvalidPhone(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store)

	_, err := svc.RegisterContact(context.Background(), Contact{
		FromID: 100, ContactUserID: 100, PhoneNumber: "12345",
	})
	require.Error(t, err)
	assert.Equal(t, apperr.KindInvalid, apperr.KindOf(err))
	assert.False(t, store.called)
}

func TestRegisterContact_UpsertsNormalizedUser(t *testing.T) {
	store := &fakeStore{created: true}
	svc := NewService(store)

	res, err := svc.RegisterContact(context.Background(), Contact{
		FromID: 42, ContactUserID: 42, PhoneNumber: "901234567",
		FirstName: "Али", LanguageCode: "uz-UZ",
	})
	require.NoError(t, err)
	assert.True(t, res.Created)
	require.True(t, store.called)
	assert.Equal(t, int64(42), store.gotUser.TelegramUserID)
	assert.Equal(t, "+998901234567", store.gotUser.Phone, "телефон нормализован в E.164")
	assert.Equal(t, "uz", store.gotUser.LanguageCode, "язык нормализован")
	assert.NotEmpty(t, store.gotUser.ID, "id сгенерирован")
	assert.Equal(t, store.gotUser.ID, res.UserID, "возвращён фактический id пользователя")
}
