package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLang(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{name: "точное uz", header: "uz", want: "uz"},
		{name: "uz с регионом", header: "uz-UZ", want: "uz"},
		{name: "uz в списке с q-параметрами", header: "ru-RU,uz;q=0.8,en;q=0.5", want: "uz"},
		{name: "верхний регистр", header: "UZ-Cyrl", want: "uz"},
		{name: "русский", header: "ru-RU", want: "ru"},
		{name: "английский по умолчанию ru", header: "en-US,en;q=0.9", want: "ru"},
		{name: "пустой заголовок", header: "", want: "ru"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := Lang(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = LangFromContext(r.Context())
			}))

			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				r.Header.Set("Accept-Language", tt.header)
			}
			h.ServeHTTP(httptest.NewRecorder(), r)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLangFromContext_Default(t *testing.T) {
	assert.Equal(t, "ru", LangFromContext(context.Background()),
		"без middleware язык по умолчанию — ru")
}
