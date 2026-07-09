package prefs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/notification/internal/domain"
	"bozor/services/notification/internal/prefs"
)

func TestEnabled_GroupNoneAlwaysOn(t *testing.T) {
	c := prefs.New("http://unused", time.Second, time.Minute)
	on, err := c.Enabled(context.Background(), "u1", domain.GroupNone)
	require.NoError(t, err)
	assert.True(t, on) // без обращения к серверу
}

func TestEnabled_ReadsAndCaches(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		assert.Equal(t, "/internal/users/u1/notification-prefs", r.URL.Path)
		_, _ = w.Write([]byte(`{"prefs":[
			{"channel":"telegram","event_type":"ad_status","enabled":false},
			{"channel":"telegram","event_type":"saved_search","enabled":true}
		]}`))
	}))
	defer srv.Close()

	c := prefs.New(srv.URL, time.Second, time.Minute)

	off, err := c.Enabled(context.Background(), "u1", domain.GroupAdStatus)
	require.NoError(t, err)
	assert.False(t, off)

	on, err := c.Enabled(context.Background(), "u1", domain.GroupSavedSearch)
	require.NoError(t, err)
	assert.True(t, on)

	// Группа не в ответе => absence = enabled.
	def, err := c.Enabled(context.Background(), "u1", domain.GroupReview)
	require.NoError(t, err)
	assert.True(t, def)

	// Все три запроса обслужены из одного HTTP-вызова (кеш по TTL).
	assert.EqualValues(t, 1, atomic.LoadInt32(&calls))
}

func TestEnabled_CacheExpires(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"prefs":[]}`))
	}))
	defer srv.Close()

	c := prefs.New(srv.URL, time.Second, time.Millisecond)
	_, err := c.Enabled(context.Background(), "u1", domain.GroupAdStatus)
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	_, err = c.Enabled(context.Background(), "u1", domain.GroupAdStatus)
	require.NoError(t, err)
	assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
}

func TestEnabled_ServerErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := prefs.New(srv.URL, time.Second, time.Minute)
	_, err := c.Enabled(context.Background(), "u1", domain.GroupAdStatus)
	assert.Error(t, err)
}
