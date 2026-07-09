package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdsSchema(t *testing.T) {
	s := AdsSchema()
	assert.Equal(t, AdsCollection, s.Name)
	assert.True(t, s.EnableNestedFields, "нужны вложенные поля для attrs.*")
	assert.Equal(t, "created_at", s.DefaultSortingField)

	byName := make(map[string]Field, len(s.Fields))
	for _, f := range s.Fields {
		byName[f.Name] = f
	}
	// Ключевые поля read-модели присутствуют с ожидаемыми свойствами.
	require.Contains(t, byName, "title")
	assert.True(t, byName["category_id"].Facet, "category_id — фасет")
	assert.True(t, byName["price"].Sort, "price — сортируемое")
	assert.True(t, byName["attrs"].Facet, "attrs — фасетируемый объект")
	assert.Equal(t, "object", byName["attrs"].Type)
	assert.Equal(t, "geopoint", byName["location"].Type)
	assert.True(t, byName["bumped_at"].Optional)
}

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, "test-key", 2*time.Second)
}

func TestHealth(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{"ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }, false},
		{"not ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":false}`)) }, true},
		{"down", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, tt.handler)
			err := c.Health(context.Background())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEnsureCollection_CreatesWhenMissing(t *testing.T) {
	var posted bool
	var gotKey string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get(apiKeyHeader)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/ads":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/collections":
			posted = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"ads"}`))
		default:
			t.Fatalf("неожиданный запрос %s %s", r.Method, r.URL.Path)
		}
	})
	created, err := c.EnsureCollection(context.Background(), AdsSchema())
	require.NoError(t, err)
	assert.True(t, created)
	assert.True(t, posted, "коллекция создана POST")
	assert.Equal(t, "test-key", gotKey, "передан API-ключ")
}

func TestEnsureCollection_SkipsWhenExists(t *testing.T) {
	var posted bool
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posted = true
		}
		if r.Method == http.MethodGet && r.URL.Path == "/collections/ads" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"ads","num_documents":5}`))
			return
		}
		t.Fatalf("неожиданный запрос %s %s", r.Method, r.URL.Path)
	})
	created, err := c.EnsureCollection(context.Background(), AdsSchema())
	require.NoError(t, err)
	assert.False(t, created)
	assert.False(t, posted, "существующая коллекция не пересоздаётся")
}

func TestEnsureCollection_ConflictIsOK(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusConflict) // гонка реплик
	})
	created, err := c.EnsureCollection(context.Background(), AdsSchema())
	require.NoError(t, err)
	assert.True(t, created)
}
