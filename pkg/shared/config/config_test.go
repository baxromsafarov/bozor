package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookup(t *testing.T) {
	t.Setenv("CFG_SET", "value")
	t.Setenv("CFG_EMPTY", "")

	tests := []struct {
		name   string
		key    string
		want   string
		wantOK bool
	}{
		{name: "заданная переменная", key: "CFG_SET", want: "value", wantOK: true},
		{name: "пустая переменная считается заданной", key: "CFG_EMPTY", want: "", wantOK: true},
		{name: "незаданная переменная", key: "CFG_ABSENT_XYZ", want: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Lookup(tt.key)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestString(t *testing.T) {
	t.Setenv("CFG_STR", "hello")
	t.Setenv("CFG_STR_EMPTY", "")

	tests := []struct {
		name string
		key  string
		def  string
		want string
	}{
		{name: "значение из окружения", key: "CFG_STR", def: "def", want: "hello"},
		{name: "пустое значение — default", key: "CFG_STR_EMPTY", def: "def", want: "def"},
		{name: "не задано — default", key: "CFG_STR_ABSENT", def: "def", want: "def"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, String(tt.key, tt.def))
		})
	}
}

func TestInt(t *testing.T) {
	t.Setenv("CFG_INT", "42")
	t.Setenv("CFG_INT_NEG", "-7")
	t.Setenv("CFG_INT_BAD", "not-a-number")

	tests := []struct {
		name string
		key  string
		def  int
		want int
	}{
		{name: "корректное число", key: "CFG_INT", def: 1, want: 42},
		{name: "отрицательное число", key: "CFG_INT_NEG", def: 1, want: -7},
		{name: "не парсится — default", key: "CFG_INT_BAD", def: 5, want: 5},
		{name: "не задано — default", key: "CFG_INT_ABSENT", def: 9, want: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Int(tt.key, tt.def))
		})
	}
}

func TestBool(t *testing.T) {
	t.Setenv("CFG_BOOL_TRUE", "true")
	t.Setenv("CFG_BOOL_ONE", "1")
	t.Setenv("CFG_BOOL_FALSE", "false")
	t.Setenv("CFG_BOOL_BAD", "yep")

	tests := []struct {
		name string
		key  string
		def  bool
		want bool
	}{
		{name: "true", key: "CFG_BOOL_TRUE", def: false, want: true},
		{name: "1 как true", key: "CFG_BOOL_ONE", def: false, want: true},
		{name: "false", key: "CFG_BOOL_FALSE", def: true, want: false},
		{name: "не парсится — default", key: "CFG_BOOL_BAD", def: true, want: true},
		{name: "не задано — default", key: "CFG_BOOL_ABSENT", def: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Bool(tt.key, tt.def))
		})
	}
}

func TestDuration(t *testing.T) {
	t.Setenv("CFG_DUR", "1h30m")
	t.Setenv("CFG_DUR_MS", "250ms")
	t.Setenv("CFG_DUR_BAD", "soon")

	tests := []struct {
		name string
		key  string
		def  time.Duration
		want time.Duration
	}{
		{name: "часы и минуты", key: "CFG_DUR", def: time.Second, want: 90 * time.Minute},
		{name: "миллисекунды", key: "CFG_DUR_MS", def: time.Second, want: 250 * time.Millisecond},
		{name: "не парсится — default", key: "CFG_DUR_BAD", def: 3 * time.Second, want: 3 * time.Second},
		{name: "не задано — default", key: "CFG_DUR_ABSENT", def: time.Minute, want: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Duration(tt.key, tt.def))
		})
	}
}

func TestMissing(t *testing.T) {
	t.Setenv("CFG_REQ_A", "a")
	t.Setenv("CFG_REQ_EMPTY", "")

	tests := []struct {
		name string
		keys []string
		want []string
	}{
		{name: "все заданы", keys: []string{"CFG_REQ_A"}, want: nil},
		{name: "пустая считается отсутствующей", keys: []string{"CFG_REQ_A", "CFG_REQ_EMPTY"}, want: []string{"CFG_REQ_EMPTY"}},
		{name: "не задана", keys: []string{"CFG_REQ_ABSENT", "CFG_REQ_A"}, want: []string{"CFG_REQ_ABSENT"}},
		{name: "без ключей", keys: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Missing(tt.keys...)
			require.Equal(t, tt.want, got)
		})
	}
}
