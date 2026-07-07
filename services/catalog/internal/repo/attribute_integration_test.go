//go:build integration

package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/pkg/shared/events"

	"bozor/services/catalog/internal/domain"
	"bozor/services/catalog/internal/repo"
)

func strptr(s string) *string { return &s }

// seedCategory создаёт категорию напрямую для тестов атрибутов.
func seedCategory(t *testing.T, r *repo.Repo, id string, parent *string, slug, path string, level int) {
	t.Helper()
	require.NoError(t, r.CreateWithEvent(context.Background(), domain.Category{
		ID: id, ParentID: parent, Slug: slug, NameUZ: slug, NameRU: slug,
		Level: level, Path: path, IsActive: true,
	}, ev(t, events.SubjectCategoryCreated, id)))
}

func TestAttributeRepo_CreateGetWithOptions(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	attrID := newID(t)
	optA, optB := newID(t), newID(t)
	require.NoError(t, r.CreateAttribute(ctx, domain.Attribute{
		ID: attrID, Slug: "deal-type", NameUZ: "Bitim", NameRU: "Сделка", Type: domain.TypeEnum,
		Options: []domain.AttributeOption{
			{ID: optA, Slug: "sale", NameUZ: "Sotish", NameRU: "Продажа", SortOrder: 1},
			{ID: optB, Slug: "rent", NameUZ: "Ijara", NameRU: "Аренда", SortOrder: 2},
		},
	}))

	got, err := r.GetAttribute(ctx, attrID)
	require.NoError(t, err)
	assert.Equal(t, domain.TypeEnum, got.Type)
	require.Len(t, got.Options, 2)
	assert.Equal(t, "sale", got.Options[0].Slug, "варианты отсортированы по sort_order")

	// Дубликат slug атрибута отклоняется.
	err = r.CreateAttribute(ctx, domain.Attribute{
		ID: newID(t), Slug: "deal-type", NameUZ: "x", NameRU: "x", Type: domain.TypeString,
	})
	assert.ErrorIs(t, err, domain.ErrAttributeSlugConflict)

	// Несуществующий атрибут.
	_, err = r.GetAttribute(ctx, newID(t))
	assert.ErrorIs(t, err, domain.ErrAttributeNotFound)
}

func TestAttributeRepo_UpdateReplacesOptions(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	attrID := newID(t)
	require.NoError(t, r.CreateAttribute(ctx, domain.Attribute{
		ID: attrID, Slug: "fuel", NameUZ: "Yoqilg'i", NameRU: "Топливо", Type: domain.TypeEnum,
		Options: []domain.AttributeOption{{ID: newID(t), Slug: "petrol", NameUZ: "Benzin", NameRU: "Бензин"}},
	}))

	require.NoError(t, r.UpdateAttribute(ctx, domain.Attribute{
		ID: attrID, Slug: "fuel", NameUZ: "Yoqilg'i turi", NameRU: "Тип топлива",
		Type: domain.TypeEnum, Unit: strptr("л"),
		Options: []domain.AttributeOption{
			{ID: newID(t), Slug: "diesel", NameUZ: "Dizel", NameRU: "Дизель", SortOrder: 1},
			{ID: newID(t), Slug: "gas", NameUZ: "Gaz", NameRU: "Газ", SortOrder: 2},
		},
	}))

	got, err := r.GetAttribute(ctx, attrID)
	require.NoError(t, err)
	assert.Equal(t, "Тип топлива", got.NameRU)
	require.NotNil(t, got.Unit)
	assert.Equal(t, "л", *got.Unit)
	require.Len(t, got.Options, 2, "старый вариант заменён на два новых")
	assert.Equal(t, "diesel", got.Options[0].Slug)
}

func TestAttributeRepo_EffectiveInheritance(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	// Дерево: transport (root) → cars (child).
	rootID, childID := newID(t), newID(t)
	seedCategory(t, r, rootID, nil, "transport", "transport", 0)
	seedCategory(t, r, childID, &rootID, "cars", "transport/cars", 1)

	// Атрибут раздела (на root) и атрибут подкатегории (на child).
	brandID, mileageID := newID(t), newID(t)
	require.NoError(t, r.CreateAttribute(ctx, domain.Attribute{
		ID: brandID, Slug: "brand", NameUZ: "Marka", NameRU: "Марка", Type: domain.TypeEnum,
		Options: []domain.AttributeOption{{ID: newID(t), Slug: "kia", NameUZ: "Kia", NameRU: "Kia"}},
	}))
	require.NoError(t, r.CreateAttribute(ctx, domain.Attribute{
		ID: mileageID, Slug: "mileage", NameUZ: "Probeg", NameRU: "Пробег", Type: domain.TypeInt, Unit: strptr("km"),
	}))

	require.NoError(t, r.LinkAttribute(ctx, rootID, brandID, 1))
	require.NoError(t, r.LinkAttribute(ctx, childID, mileageID, 1))

	// Эффективные атрибуты child = унаследованный brand (от root) + собственный mileage.
	eff, err := r.EffectiveAttributes(ctx, childID)
	require.NoError(t, err)
	require.Len(t, eff, 2)

	byID := map[string]domain.EffectiveAttribute{}
	for _, e := range eff {
		byID[e.ID] = e
	}
	assert.True(t, byID[brandID].Inherited, "brand унаследован от раздела")
	assert.Equal(t, rootID, byID[brandID].SourceID)
	require.Len(t, byID[brandID].Options, 1, "варианты enum подгружены и в эффективном наборе")
	assert.False(t, byID[mileageID].Inherited, "mileage — собственный атрибут подкатегории")
	assert.Equal(t, childID, byID[mileageID].SourceID)

	// У root — только собственный brand.
	rootEff, err := r.EffectiveAttributes(ctx, rootID)
	require.NoError(t, err)
	require.Len(t, rootEff, 1)
	assert.Equal(t, brandID, rootEff[0].ID)
	assert.False(t, rootEff[0].Inherited)

	// Повторная привязка — конфликт.
	err = r.LinkAttribute(ctx, rootID, brandID, 5)
	assert.ErrorIs(t, err, domain.ErrLinkExists)

	// Снятие привязки.
	require.NoError(t, r.UnlinkAttribute(ctx, childID, mileageID))
	err = r.UnlinkAttribute(ctx, childID, mileageID)
	assert.ErrorIs(t, err, domain.ErrLinkNotFound)

	after, err := r.EffectiveAttributes(ctx, childID)
	require.NoError(t, err)
	require.Len(t, after, 1, "после снятия остаётся только унаследованный brand")
	assert.True(t, after[0].Inherited)
}

func TestAttributeRepo_DeleteCascades(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	r := repo.NewRepo(pool)

	catID := newID(t)
	seedCategory(t, r, catID, nil, "electronics", "electronics", 0)

	attrID := newID(t)
	require.NoError(t, r.CreateAttribute(ctx, domain.Attribute{
		ID: attrID, Slug: "condition", NameUZ: "Holati", NameRU: "Состояние", Type: domain.TypeEnum,
		Options: []domain.AttributeOption{{ID: newID(t), Slug: "new", NameUZ: "Yangi", NameRU: "Новый"}},
	}))
	require.NoError(t, r.LinkAttribute(ctx, catID, attrID, 1))

	require.NoError(t, r.DeleteAttribute(ctx, attrID))

	// Варианты и привязки ушли каскадом.
	var opts, links int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM attribute_options WHERE attribute_id = $1", attrID).Scan(&opts))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM category_attributes WHERE attribute_id = $1", attrID).Scan(&links))
	assert.Zero(t, opts)
	assert.Zero(t, links)

	err := r.DeleteAttribute(ctx, attrID)
	assert.ErrorIs(t, err, domain.ErrAttributeNotFound)
}
