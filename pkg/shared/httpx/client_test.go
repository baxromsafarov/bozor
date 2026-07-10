package httpx

import (
	"net/http"
	"testing"
	"time"
)

func TestPooledTransport_Tuning(t *testing.T) {
	tr := PooledTransport()
	if tr.MaxIdleConnsPerHost <= 2 {
		t.Fatalf("MaxIdleConnsPerHost=%d, ожидался пул > дефолтных 2", tr.MaxIdleConnsPerHost)
	}
	if tr.MaxIdleConns < tr.MaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConns=%d < MaxIdleConnsPerHost=%d", tr.MaxIdleConns, tr.MaxIdleConnsPerHost)
	}
	if tr.DialContext == nil {
		t.Fatal("DialContext не задан (нужен явный таймаут/keepalive)")
	}
	if tr.IdleConnTimeout == 0 {
		t.Fatal("IdleConnTimeout не задан")
	}
}

func TestNewClient_UsesPooledTransport(t *testing.T) {
	c := NewClient(5 * time.Second)
	if c.Timeout != 5*time.Second {
		t.Fatalf("Timeout=%v, ожидался 5s", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport типа %T, ожидался *http.Transport", c.Transport)
	}
	if tr.MaxIdleConnsPerHost != poolMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost=%d, ожидался %d", tr.MaxIdleConnsPerHost, poolMaxIdleConnsPerHost)
	}
}
