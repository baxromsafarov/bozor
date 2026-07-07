package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"из RemoteAddr", "203.0.113.7:54321", "", "203.0.113.7"},
		{"первый из X-Forwarded-For", "10.0.0.1:100", "198.51.100.9, 10.0.0.1", "198.51.100.9"},
		{"единственный X-Forwarded-For", "10.0.0.1:100", "198.51.100.9", "198.51.100.9"},
		{"RemoteAddr без порта", "203.0.113.7", "", "203.0.113.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			assert.Equal(t, tt.want, clientIP(r))
		})
	}
}

func TestRateLimitKey(t *testing.T) {
	t.Run("по пользователю, если аутентифицирован", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set(headerUserID, "u-1")
		assert.Equal(t, "rl:user:u-1", rateLimitKey(r))
	})
	t.Run("по IP для анонима", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "203.0.113.7:5"
		assert.Equal(t, "rl:ip:203.0.113.7", rateLimitKey(r))
	})
}

func TestCORS_AllowlistOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"https://bozor.uz"})(next)

	t.Run("разрешённый источник отражается", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/ads", nil)
		r.Header.Set("Origin", "https://bozor.uz")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		assert.Equal(t, "https://bozor.uz", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Contains(t, rec.Header().Values("Vary"), "Origin")
	})

	t.Run("посторонний источник не отражается", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/ads", nil)
		r.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestStripIdentityHeaders(t *testing.T) {
	var seenUser, seenRoles string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenUser = r.Header.Get(headerUserID)
		seenRoles = r.Header.Get(headerUserRoles)
	})
	h := StripIdentityHeaders(next)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(headerUserID, "attacker")
	r.Header.Set(headerUserRoles, "admin")
	h.ServeHTTP(httptest.NewRecorder(), r)

	assert.Empty(t, seenUser)
	assert.Empty(t, seenRoles)
}
