package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		from, to Status
		ok       bool
	}{
		{StatusDraft, StatusPending, true},
		{StatusDraft, StatusActive, false},
		{StatusPending, StatusActive, true},
		{StatusPending, StatusRejected, true},
		{StatusRejected, StatusPending, true}, // после правки — снова на модерацию
		{StatusActive, StatusSold, true},
		{StatusActive, StatusExpired, true},
		{StatusActive, StatusArchived, true},
		{StatusActive, StatusPending, true},   // повторная модерация после правки активного
		{StatusExpired, StatusActive, true},   // реактивация продлением (renew)
		{StatusExpired, StatusPending, false}, // истёкшее нельзя сразу на модерацию
		{StatusSold, StatusActive, false},     // терминальный
		{StatusActive, StatusBlocked, true},
		{StatusDraft, StatusBlocked, true}, // blocked — из любого
		{StatusSold, StatusBlocked, true},  // включая терминальные
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.ok, tt.from.CanTransitionTo(tt.to), "%s→%s", tt.from, tt.to)
	}
}

func TestStatus_Valid(t *testing.T) {
	assert.True(t, StatusActive.Valid())
	assert.False(t, Status("weird").Valid())
}

func TestAd_ValidateCore(t *testing.T) {
	base := func() *Ad {
		return &Ad{Title: "Телефон", Price: 1000, CategoryID: "cat", RegionID: 1}
	}
	assert.NoError(t, base().ValidateCore())

	a := base()
	a.Title = ""
	assert.ErrorIs(t, a.ValidateCore(), ErrEmptyTitle)

	a = base()
	a.Price = -1
	assert.ErrorIs(t, a.ValidateCore(), ErrNegativePrice)

	a = base()
	a.CategoryID = ""
	assert.ErrorIs(t, a.ValidateCore(), ErrMissingCategory)

	a = base()
	a.RegionID = 0
	assert.ErrorIs(t, a.ValidateCore(), ErrMissingRegion)
}

func TestValidateImages(t *testing.T) {
	assert.NoError(t, ValidateImages([]AdImage{{MediaID: "a", IsCover: true}, {MediaID: "b"}}))

	two := []AdImage{{MediaID: "a", IsCover: true}, {MediaID: "b", IsCover: true}}
	assert.ErrorIs(t, ValidateImages(two), ErrMultipleCovers)

	many := make([]AdImage, MaxImages+1)
	assert.ErrorIs(t, ValidateImages(many), ErrTooManyImages)
}

func TestValidateAttributes(t *testing.T) {
	specs := []AttrSpec{
		{Slug: "brand", Type: AttrEnum, Required: true, Options: []string{"bmw", "audi"}},
		{Slug: "year", Type: AttrInt, Required: false},
		{Slug: "negotiable", Type: AttrBool, Required: false},
	}

	// Валидный набор.
	assert.NoError(t, ValidateAttributes(specs, []AdAttributeValue{
		{AttributeSlug: "brand", Value: "bmw"},
		{AttributeSlug: "year", Value: "2015"},
		{AttributeSlug: "negotiable", Value: "true"},
	}))

	// Отсутствует обязательный brand.
	assert.ErrorIs(t, ValidateAttributes(specs, []AdAttributeValue{
		{AttributeSlug: "year", Value: "2015"},
	}), ErrMissingRequiredAttr)

	// Неизвестный атрибут.
	assert.ErrorIs(t, ValidateAttributes(specs, []AdAttributeValue{
		{AttributeSlug: "brand", Value: "bmw"},
		{AttributeSlug: "color", Value: "red"},
	}), ErrUnknownAttribute)

	// enum вне списка вариантов.
	assert.ErrorIs(t, ValidateAttributes(specs, []AdAttributeValue{
		{AttributeSlug: "brand", Value: "lada"},
	}), ErrInvalidAttrValue)

	// int с нечисловым значением.
	assert.ErrorIs(t, ValidateAttributes(specs, []AdAttributeValue{
		{AttributeSlug: "brand", Value: "bmw"},
		{AttributeSlug: "year", Value: "две тысячи"},
	}), ErrInvalidAttrValue)

	// bool с недопустимым значением.
	assert.ErrorIs(t, ValidateAttributes(specs, []AdAttributeValue{
		{AttributeSlug: "brand", Value: "audi"},
		{AttributeSlug: "negotiable", Value: "maybe"},
	}), ErrInvalidAttrValue)
}
