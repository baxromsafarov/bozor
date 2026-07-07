package domain

import "errors"

// Доменные ошибки атрибутов.
var (
	ErrAttributeNotFound     = errors.New("атрибут не найден")
	ErrAttributeSlugConflict = errors.New("атрибут с таким slug уже существует")
	ErrOptionSlugConflict    = errors.New("вариант с таким slug уже существует у атрибута")
	ErrInvalidAttributeType  = errors.New("недопустимый тип атрибута")
	ErrEnumRequiresOptions   = errors.New("для enum-атрибута нужны варианты значений")
	ErrOptionsForbidden      = errors.New("варианты значений допустимы только для enum-атрибута")
	ErrLinkExists            = errors.New("атрибут уже привязан к категории")
	ErrLinkNotFound          = errors.New("привязка атрибута к категории не найдена")
)

// AttributeType — тип значения атрибута объявления.
type AttributeType string

// Допустимые типы атрибутов.
const (
	TypeEnum    AttributeType = "enum"
	TypeInt     AttributeType = "int"
	TypeDecimal AttributeType = "decimal"
	TypeBool    AttributeType = "bool"
	TypeString  AttributeType = "string"
)

// Valid сообщает, поддерживается ли тип.
func (t AttributeType) Valid() bool {
	switch t {
	case TypeEnum, TypeInt, TypeDecimal, TypeBool, TypeString:
		return true
	default:
		return false
	}
}

// AttributeOption — вариант значения enum-атрибута.
type AttributeOption struct {
	ID        string
	Slug      string
	NameUZ    string
	NameRU    string
	SortOrder int
}

// Attribute — определение атрибута (характеристики объявления).
type Attribute struct {
	ID           string
	Slug         string
	NameUZ       string
	NameRU       string
	Type         AttributeType
	Unit         *string
	IsRequired   bool
	IsFilterable bool
	Options      []AttributeOption // заполняется для enum
}

// EffectiveAttribute — атрибут в контексте конкретной категории с учётом
// наследования: собственный или пришедший от предка по материализованному path.
type EffectiveAttribute struct {
	Attribute
	SortOrder int
	Inherited bool   // true, если атрибут задан на категории-предке
	SourceID  string // id категории, на которой атрибут привязан
}
