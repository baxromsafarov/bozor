package httpx

import (
	"net"
	"net/http"
	"time"
)

// Пул исходящих соединений для межсервисных HTTP-клиентов.
const (
	poolMaxIdleConns        = 512
	poolMaxIdleConnsPerHost = 128
	poolIdleConnTimeout     = 90 * time.Second
	poolDialTimeout         = 3 * time.Second
	poolDialKeepAlive       = 30 * time.Second
)

// PooledTransport возвращает *http.Transport с пулом keepalive-соединений,
// пригодный для межсервисных клиентов под нагрузкой.
//
// Дефолтный http.DefaultTransport держит лишь MaxIdleConnsPerHost=2: под
// конкуренцией это приводит к постоянному открытию/закрытию TCP-соединений к
// апстриму и исчерпанию эфемерных портов («connect: cannot assign requested
// address», 5xx). Расширенный пул переиспользует соединения. Выявлено
// нагрузочным тестом k6 (Stage 10.1): search → Typesense падал именно так.
func PooledTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   poolDialTimeout,
			KeepAlive: poolDialKeepAlive,
		}).DialContext,
		MaxIdleConns:          poolMaxIdleConns,
		MaxIdleConnsPerHost:   poolMaxIdleConnsPerHost,
		IdleConnTimeout:       poolIdleConnTimeout,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// NewClient возвращает *http.Client с пулом keepalive-соединений
// (PooledTransport) и общим таймаутом на запрос. Использовать для межсервисных
// вызовов вместо &http.Client{Timeout: ...} — тот берёт http.DefaultTransport
// с MaxIdleConnsPerHost=2 и под нагрузкой выжигает эфемерные порты.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: PooledTransport()}
}
