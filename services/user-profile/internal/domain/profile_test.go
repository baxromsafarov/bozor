package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserType_Valid(t *testing.T) {
	assert.True(t, UserTypeIndividual.Valid())
	assert.True(t, UserTypeBusiness.Valid())
	assert.False(t, UserType("company").Valid())
}

func TestNewDefaultProfile(t *testing.T) {
	now := time.Now().UTC()
	p := NewDefaultProfile("u1", "", now)
	assert.Equal(t, UserTypeIndividual, p.UserType)
	assert.True(t, p.ContactPhoneVisible)
	assert.Equal(t, "ru", p.LanguageCode, "язык по умолчанию при пустом")
	assert.Equal(t, now, p.CreatedAt)

	assert.Equal(t, "uz", NewDefaultProfile("u1", "uz", now).LanguageCode)
}

func TestProfile_Validate(t *testing.T) {
	base := NewDefaultProfile("u1", "ru", time.Now().UTC())

	tests := []struct {
		name    string
		mutate  func(p *Profile)
		wantErr error
	}{
		{"валидный individual", func(*Profile) {}, nil},
		{"валидный business", func(p *Profile) { p.UserType = UserTypeBusiness; p.BusinessName = "ООО Ромашка" }, nil},
		{"неизвестный тип", func(p *Profile) { p.UserType = "company" }, ErrInvalidUserType},
		{"business без названия", func(p *Profile) { p.UserType = UserTypeBusiness }, ErrBusinessNameRequired},
		{"длинное имя", func(p *Profile) { p.DisplayName = strings.Repeat("я", MaxDisplayName+1) }, ErrDisplayNameTooLong},
		{"длинное описание", func(p *Profile) { p.About = strings.Repeat("a", MaxAbout+1) }, ErrAboutTooLong},
		{"длинное название бизнеса", func(p *Profile) {
			p.UserType = UserTypeBusiness
			p.BusinessName = strings.Repeat("b", MaxBusinessName+1)
		}, ErrBusinessNameTooLong},
		{"имя на границе", func(p *Profile) { p.DisplayName = strings.Repeat("я", MaxDisplayName) }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base
			tt.mutate(&p)
			err := p.Validate()
			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}
