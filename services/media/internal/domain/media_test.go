package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateUpload(t *testing.T) {
	lim := Limits{MaxSizeBytes: 100, MaxPerAd: 5}

	tests := []struct {
		name string
		mime string
		size int64
		want error
	}{
		{"пустой файл", "image/jpeg", 0, ErrEmptyFile},
		{"неподдерживаемый тип", "application/pdf", 50, ErrUnsupportedType},
		{"слишком большой", "image/png", 101, ErrFileTooLarge},
		{"валидный jpeg", "image/jpeg", 100, nil},
		{"валидный png", "image/png", 10, nil},
		{"валидный webp", "image/webp", 10, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorIs(t, ValidateUpload(tt.mime, tt.size, lim), tt.want)
		})
	}
}

func TestExtFor(t *testing.T) {
	ext, ok := ExtFor("image/jpeg")
	assert.True(t, ok)
	assert.Equal(t, "jpg", ext)

	_, ok = ExtFor("image/gif")
	assert.False(t, ok, "gif не поддерживается")
}
