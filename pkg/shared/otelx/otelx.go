// Package otelx содержит вспомогательные функции для инициализации
// OpenTelemetry-трассировки в сервисах Bozor.
package otelx

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

// Setup инициализирует OpenTelemetry-трассировку для сервиса.
//
// Endpoint коллектора берётся из переменной окружения
// OTEL_EXPORTER_OTLP_ENDPOINT. Пропагатор контекста (tracecontext + baggage)
// устанавливается всегда. Если endpoint пуст, экспортёр не создаётся и
// возвращается no-op shutdown-функция. Возвращаемую shutdown-функцию
// необходимо вызвать при остановке сервиса для сброса буферов трасс.
func Setup(ctx context.Context, service string) (func(context.Context) error, error) {
	// Пропагатор нужен даже без экспортёра: сервис должен корректно
	// пробрасывать trace-контекст дальше по цепочке вызовов.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		// Экспортёр не настроен — возвращаем безопасную заглушку.
		return func(context.Context) error { return nil }, nil
	}

	// otlptracegrpc ожидает endpoint вида host:port, без схемы.
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otelx: создание OTLP-экспортёра: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(service)),
	)
	if err != nil {
		return nil, fmt.Errorf("otelx: создание resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// HTTPMiddleware возвращает HTTP-middleware, оборачивающий обработчики
// в otelhttp.NewHandler для автоматической трассировки входящих запросов.
func HTTPMiddleware(service string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, service)
	}
}
