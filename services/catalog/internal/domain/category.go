// Package domain содержит доменные сущности и правила Catalog-сервиса.
package domain

import "errors"

// Доменные ошибки каталога.
var (
	ErrCategoryNotFound = errors.New("категория не найдена")
	ErrParentNotFound   = errors.New("родительская категория не найдена")
	ErrHasChildren      = errors.New("у категории есть подкатегории")
	ErrSlugConflict     = errors.New("категория с таким slug уже существует")
)

// Category — узел дерева категорий.
type Category struct {
	ID        string
	ParentID  *string
	Slug      string
	NameUZ    string
	NameRU    string
	Level     int
	Path      string
	SortOrder int
	IsActive  bool
}

// TreeNode — узел дерева категорий с потомками.
type TreeNode struct {
	Category
	Children []*TreeNode
}

// BuildTree собирает дерево из плоского списка. Порядок детей сохраняется из
// входного среза (репозиторий отдаёт его отсортированным по level, sort_order).
func BuildTree(cats []Category) []*TreeNode {
	nodes := make(map[string]*TreeNode, len(cats))
	for i := range cats {
		nodes[cats[i].ID] = &TreeNode{Category: cats[i]}
	}
	var roots []*TreeNode
	for i := range cats {
		n := nodes[cats[i].ID]
		if cats[i].ParentID == nil {
			roots = append(roots, n)
			continue
		}
		if parent, ok := nodes[*cats[i].ParentID]; ok {
			parent.Children = append(parent.Children, n)
		} else {
			roots = append(roots, n) // родитель вне выборки — считаем корнем
		}
	}
	return roots
}
