package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// InitTracer initialises the global OTel tracer provider and returns a shutdown function.
// If endpoint is empty the tracer is a no-op (useful in local dev).
func InitTracer(ctx context.Context, endpoint, serviceName string) (func(), error) {
	if endpoint == "" {
		return func() {}, nil
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exp),
		trace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String("1.0.0"),
		)),
		trace.WithSampler(trace.ParentBased(trace.TraceIDRatioBased(0.1))),
	)
	otel.SetTracerProvider(tp)

	return func() { _ = tp.Shutdown(context.Background()) }, nil
}
