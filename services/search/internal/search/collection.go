package search

// AdsCollection — имя коллекции объявлений в Typesense.
const AdsCollection = "ads"

// Типы полей Typesense.
const (
	typeString   = "string"
	typeInt32    = "int32"
	typeInt64    = "int64"
	typeBool     = "bool"
	typeObject   = "object"
	typeGeopoint = "geopoint"
)

// Field — поле схемы коллекции Typesense.
type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Facet    bool   `json:"facet,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	Sort     bool   `json:"sort,omitempty"`
	Locale   string `json:"locale,omitempty"`
}

// CollectionSchema — схема коллекции Typesense.
type CollectionSchema struct {
	Name                string  `json:"name"`
	Fields              []Field `json:"fields"`
	DefaultSortingField string  `json:"default_sorting_field,omitempty"`
	EnableNestedFields  bool    `json:"enable_nested_fields,omitempty"`
}

// AdsSchema возвращает схему коллекции ads (read-модель поиска, ARCHITECTURE §4.7):
// полнотекст title/description, фасеты категории/локации/валюты/статуса и
// динамических атрибутов (attrs.*), сортировки по времени/цене/промо, гео.
func AdsSchema() CollectionSchema {
	return CollectionSchema{
		Name:               AdsCollection,
		EnableNestedFields: true, // для фасетов по attrs.<slug>
		Fields: []Field{
			{Name: "title", Type: typeString},
			{Name: "description", Type: typeString, Optional: true},
			{Name: "category_id", Type: typeString, Facet: true},
			{Name: "category_path", Type: typeString, Facet: true, Optional: true},
			{Name: "region_id", Type: typeInt32, Facet: true},
			{Name: "city_id", Type: typeInt64, Facet: true, Optional: true},
			{Name: "price", Type: typeInt64, Sort: true},
			{Name: "currency", Type: typeString, Facet: true},
			{Name: "status", Type: typeString, Facet: true},
			// Динамические атрибуты объявления — вложенный объект attrs.<slug>.
			{Name: "attrs", Type: typeObject, Facet: true, Optional: true},
			{Name: "created_at", Type: typeInt64, Sort: true},
			{Name: "bumped_at", Type: typeInt64, Sort: true, Optional: true},
			{Name: "is_top", Type: typeBool, Facet: true},
			{Name: "promotion_rank", Type: typeInt32, Sort: true, Optional: true},
			// Момент окончания промо (unix-сек) — фильтр «активные TOP» топ-блока (8.6).
			{Name: "promo_ends_at", Type: typeInt64, Optional: true},
			{Name: "location", Type: typeGeopoint, Optional: true},
		},
		DefaultSortingField: "created_at",
	}
}
