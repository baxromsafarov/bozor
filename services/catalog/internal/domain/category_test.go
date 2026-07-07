package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTree(t *testing.T) {
	p := "p"
	cats := []Category{
		{ID: "p", ParentID: nil, Slug: "electronics", Level: 0},
		{ID: "c1", ParentID: &p, Slug: "phones", Level: 1},
		{ID: "c2", ParentID: &p, Slug: "laptops", Level: 1},
	}
	roots := BuildTree(cats)

	require.Len(t, roots, 1)
	assert.Equal(t, "p", roots[0].ID)
	require.Len(t, roots[0].Children, 2)
	assert.Equal(t, "c1", roots[0].Children[0].ID, "порядок детей сохраняется из входного списка")
	assert.Equal(t, "c2", roots[0].Children[1].ID)
	assert.Empty(t, roots[0].Children[0].Children)
}

func TestBuildTree_Empty(t *testing.T) {
	assert.Empty(t, BuildTree(nil))
}

func TestBuildTree_DanglingParentBecomesRoot(t *testing.T) {
	missing := "missing"
	roots := BuildTree([]Category{{ID: "c", ParentID: &missing}})
	require.Len(t, roots, 1, "узел с отсутствующим родителем трактуется как корень")
	assert.Equal(t, "c", roots[0].ID)
}
