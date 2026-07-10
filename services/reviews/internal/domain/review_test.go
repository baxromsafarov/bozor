package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRating(t *testing.T) {
	for _, r := range []int{1, 2, 3, 4, 5} {
		assert.NoError(t, ValidateRating(r))
	}
	assert.ErrorIs(t, ValidateRating(0), ErrInvalidRating)
	assert.ErrorIs(t, ValidateRating(6), ErrInvalidRating)
	assert.ErrorIs(t, ValidateRating(-1), ErrInvalidRating)
}

func TestNormalizeBody(t *testing.T) {
	got, err := NormalizeBody("  хороший продавец  ")
	require.NoError(t, err)
	assert.Equal(t, "хороший продавец", got, "пробелы по краям обрезаны")

	empty, err := NormalizeBody("   ")
	require.NoError(t, err)
	assert.Empty(t, empty, "пустой текст допустим (отзыв только с оценкой)")

	_, err = NormalizeBody(strings.Repeat("я", MaxBodyLen+1))
	assert.ErrorIs(t, err, ErrBodyTooLong)

	ok, err := NormalizeBody(strings.Repeat("я", MaxBodyLen))
	require.NoError(t, err, "ровно предел — допустимо")
	assert.Len(t, []rune(ok), MaxBodyLen)
}
