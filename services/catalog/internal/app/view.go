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
