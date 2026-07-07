package app

import "bozor/services/catalog/internal/domain"

// categoryView — узел дерева в ответе API (сериализуется в JSON и кешируется).
type categoryView struct {
	ID        string         `json:"id"`
	ParentID  *string        `json:"parent_id,omitempty"`
	Slug      string         `json:"slug"`
	NameUZ    string         `json:"name_uz"`
	NameRU    string         `json:"name_ru"`
	Level     int            `json:"level"`
	SortOrder int            `json:"sort_order"`
	IsActive  bool           `json:"is_active"`
	Children  []categoryView `json:"children,omitempty"`
}

// toViews превращает дерево доменных узлов в срез view-моделей.
func toViews(nodes []*domain.TreeNode) []categoryView {
	views := make([]categoryView, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, categoryView{
			ID: n.ID, ParentID: n.ParentID, Slug: n.Slug,
			NameUZ: n.NameUZ, NameRU: n.NameRU, Level: n.Level,
			SortOrder: n.SortOrder, IsActive: n.IsActive,
			Children: toViews(n.Children),
		})
	}
	return views
}

// optionView — вариант enum-значения в кешируемом ответе.
type optionView struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	NameUZ    string `json:"name_uz"`
	NameRU    string `json:"name_ru"`
	SortOrder int    `json:"sort_order"`
}

// effectiveAttrView — эффективный атрибут категории в кешируемом ответе.
type effectiveAttrView struct {
	ID           string       `json:"id"`
	Slug         string       `json:"slug"`
	NameUZ       string       `json:"name_uz"`
	NameRU       string       `json:"name_ru"`
	Type         string       `json:"type"`
	Unit         *string      `json:"unit,omitempty"`
	IsRequired   bool         `json:"is_required"`
	IsFilterable bool         `json:"is_filterable"`
	Inherited    bool         `json:"inherited"`
	SortOrder    int          `json:"sort_order"`
	SourceID     string       `json:"source_id"`
	Options      []optionView `json:"options,omitempty"`
}

// toEffectiveViews превращает эффективные атрибуты в срез view-моделей.
func toEffectiveViews(items []domain.EffectiveAttribute) []effectiveAttrView {
	views := make([]effectiveAttrView, 0, len(items))
	for _, e := range items {
		v := effectiveAttrView{
			ID: e.ID, Slug: e.Slug, NameUZ: e.NameUZ, NameRU: e.NameRU,
			Type: string(e.Type), Unit: e.Unit,
			IsRequired: e.IsRequired, IsFilterable: e.IsFilterable,
			Inherited: e.Inherited, SortOrder: e.SortOrder, SourceID: e.SourceID,
		}
		for _, o := range e.Options {
			v.Options = append(v.Options, optionView{
				ID: o.ID, Slug: o.Slug, NameUZ: o.NameUZ, NameRU: o.NameRU, SortOrder: o.SortOrder,
			})
		}
		views = append(views, v)
	}
	return views
}
