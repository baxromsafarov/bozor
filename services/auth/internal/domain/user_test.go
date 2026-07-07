package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePhoneUZ(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"международный с плюсом", "+998901234567", "+998901234567", false},
		{"международный без плюса", "998901234567", "+998901234567", false},
		{"с пробелами и скобками", "+998 (90) 123-45-67", "+998901234567", false},
		{"национальный 9 цифр", "901234567", "+998901234567", false},
		{"слишком короткий", "12345", "", true},
		{"иностранный 11 цифр", "+79001234567", "", true},
		{"пустой", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePhoneUZ(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeLang(t *testing.T) {
	assert.Equal(t, "uz", NormalizeLang("uz"))
	assert.Equal(t, "uz", NormalizeLang("uz-UZ"))
	assert.Equal(t, "ru", NormalizeLang("ru"))
	assert.Equal(t, "ru", NormalizeLang("en"))
	assert.Equal(t, "ru", NormalizeLang(""))
}
