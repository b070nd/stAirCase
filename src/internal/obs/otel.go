// Package obs — OpenTelemetry integration (CHECK 10.3.1).
//
// InitOTel configures an OTLP/gRPC trace exporter when --otel-endpoint is
// set.  Without an endpoint this is a cheap no-op; the flag is opt-in so
// operators choose whether to pay the observability overhead.
package obs

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// OTelEndpoint holds the OTLP endpoint configured via --otel-endpoint
// (empty string = OTel disabled).  Set by [InitOTel] before the runner starts.
var OTelEndpoint string

// Tracer is the tracer all stAirCase spans are created from. It is a no-op
// tracer until InitOTel is called with a non-empty endpoint, so instrumented
// code paths never need to check whether OTel is enabled.
var Tracer trace.Tracer = noop.NewTracerProvider().Tracer("staircase")

// InitOTel configures the OpenTelemetry SDK for the given endpoint
// (CHECK 10.3.1).  When endpoint is empty this is a no-op and a no-op
// shutdown function is returned.  The returned function must be called (defer)
// on process exit to guarantee span delivery.
//
// The exporter connects lazily and asynchronously: an unreachable collector
// never blocks or fails a run — spans are dropped after the batch timeout.
func InitOTel(endpoint string) (shutdown func()) {
	OTelEndpoint = endpoint
	if endpoint == "" {
		Tracer = noop.NewTracerProvider().Tracer("staircase")
		return func() {}
	}

	exporter, err := otlptracegrpc.New(context.Background(),
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(), // local collectors; TLS endpoints via OTEL_EXPORTER_OTLP_* env
	)
	if err != nil {
		Log.Warn("otel exporter init failed — tracing disabled", "endpoint", endpoint, "err", err)
		return func() {}
	}

	res, _ := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("staircase"),
	))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	Tracer = tp.Tracer("staircase")

	Log.Info("otel enabled", "endpoint", endpoint)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			Log.Warn("otel shutdown", "err", err)
		}
	}
}
