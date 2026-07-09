package domain

import (
	"slices"
	"strconv"
)

// AttrType — тип атрибута (зеркалит типы Catalog).
type AttrType string

// Типы атрибутов Catalog.
const (
	AttrEnum    AttrType = "enum"
	AttrInt     AttrType = "int"
	AttrDecimal AttrType = "decimal"
	AttrBool    AttrType = "bool"
	AttrString  AttrType = "string"
)

// AttrSpec — спецификация атрибута из эффективного набора Catalog для категории.
// Получается через CatalogValidator (gRPC) и служит правилом валидации значений.
type AttrSpec struct {
	Slug     string
	Type     AttrType
	Required bool
	Options  []string // допустимые slug'и вариантов для типа enum
}

// ValidateAttributes проверяет значения объявления против набора спецификаций
// категории: неизвестные атрибуты запрещены, обязательные — обязательны, а
// значение должно соответствовать типу (и списку вариантов для enum).
func ValidateAttributes(specs []AttrSpec, values []AdAttributeValue) error {
	specBySlug := make(map[string]AttrSpec, len(specs))
	for _, s := range specs {
		specBySlug[s.Slug] = s
	}

	provided := make(map[string]bool, len(values))
	for _, v := range values {
		spec, ok := specBySlug[v.AttributeSlug]
		if !ok {
			return ErrUnknownAttribute
		}
		if !validValue(spec, v.Value) {
			return ErrInvalidAttrValue
		}
		provided[v.AttributeSlug] = true
	}

	for _, s := range specs {
		if s.Required && !provided[s.Slug] {
			return ErrMissingRequiredAttr
		}
	}
	return nil
}

// validValue проверяет соответствие значения типу атрибута.
func validValue(spec AttrSpec, value string) bool {
	switch spec.Type {
	case AttrInt:
		_, err := strconv.ParseInt(value, 10, 64)
		return err == nil
	case AttrDecimal:
		_, err := strconv.ParseFloat(value, 64)
		return err == nil
	case AttrBool:
		return value == "true" || value == "false"
	case AttrEnum:
		return slices.Contains(spec.Options, value)
	case AttrString:
		return value != ""
	default:
		return false
	}
}
