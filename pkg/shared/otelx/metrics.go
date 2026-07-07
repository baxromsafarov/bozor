package otelx

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

// SetupMetrics устанавливает глобальный OTel MeterProvider с Prometheus-
// экспортёром и возвращает HTTP-обработчик для эндпоинта /metrics плюс
// функцию остановки. После вызова HTTP-инструментация (HTTPMiddleware поверх
// otelhttp) автоматически пишет RED-метрики HTTP-сервера в этот провайдер;
// Prometheus собирает их по /metrics вместе со стандартными go_*/process_*
// метриками (полезны для USE-панелей).
func SetupMetrics(service string) (http.Handler, func(context.Context) error, error) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	exporter, err := otelprom.New(otelprom.WithRegisterer(reg))
	if err != nil {
		return nil, nil, fmt.Errorf("otelx: prometheus-экспортёр: %w", err)
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(semconv.ServiceName(service)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("otelx: resource для метрик: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{}), mp.Shutdown, nil
}
