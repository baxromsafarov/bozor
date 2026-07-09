package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"bozor/services/moderation/internal/domain"
)

func TestNormalize(t *testing.T) {
	assert.Equal(t, "копия iphone", domain.Normalize("  Копия   iPhone  "))
	assert.Equal(t, "под оригинал", domain.Normalize("Под   Оригинал"))
	assert.Equal(t, "все еще", domain.Normalize("Всё ещё")) // ё→е
}

func TestContentHash_StableAndNormalized(t *testing.T) {
	h1 := domain.ContentHash("iPhone 15", "Новый телефон")
	h2 := domain.ContentHash("  iphone   15 ", "новый  телефон")
	assert.Equal(t, h1, h2, "хэш инвариантен к регистру/пробелам")

	h3 := domain.ContentHash("iPhone 14", "Новый телефон")
	assert.NotEqual(t, h1, h3, "разное содержимое → разный хэш")
}

func TestMatchedStopwords(t *testing.T) {
	words := []string{"копия", "реплика", "под оригинал", "soxta"}

	matched := domain.MatchedStopwords("Копия iPhone", "Отличная реплика, под оригинал", words)
	assert.ElementsMatch(t, []string{"копия", "реплика", "под оригинал"}, matched)

	// В описании — узбекское стоп-слово.
	assert.Equal(t, []string{"soxta"}, domain.MatchedStopwords("Soat", "soxta mahsulot", words))

	// Чисто — ничего.
	assert.Empty(t, domain.MatchedStopwords("Новый оригинальный телефон", "гарантия", words))
}

func TestMatchedStopwords_NoDuplicateMatch(t *testing.T) {
	// Слово встречается дважды — в результате один раз.
	matched := domain.MatchedStopwords("копия копия", "копия", []string{"копия", "копия"})
	assert.Equal(t, []string{"копия"}, matched)
}

func TestEvaluate(t *testing.T) {
	// Всё чисто → нет причин.
	assert.Empty(t, domain.Evaluate(nil, false, false))

	// Стоп-слова + категория + дубль.
	reasons := domain.Evaluate([]string{"копия"}, true, true)
	assert.Equal(t, []string{
		domain.ReasonStopwordPrefix + "копия",
		domain.ReasonForbiddenCat,
		domain.ReasonDuplicate,
	}, reasons)
}
