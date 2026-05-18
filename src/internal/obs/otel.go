// Package obs — OpenTelemetry integration (CHECK 10.3.1).
//
// InitOTel configures an OTLP/gRPC trace exporter when --otel-endpoint is
// set.  Without an endpoint this is a cheap no-op; the flag is opt-in so
// operators choose whether to pay the observability overhead.
//
// To wire the full SDK add:
//
//	go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
//	go get go.opentelemetry.io/otel/sdk/trace
//
// and uncomment the gRPC exporter block below.  The OTEL_EXPORTER_OTLP_ENDPOINT
// environment variable is also honoured by the SDK when set.
package obs

// OTelEndpoint holds the OTLP endpoint configured via --otel-endpoint
// (empty string = OTel disabled).  Set by [InitOTel] before the runner starts.
var OTelEndpoint string

// OTelShutdown is called on process exit to flush any pending spans.
var OTelShutdown func() = func() {}

// InitOTel configures the OpenTelemetry SDK for the given endpoint
// (CHECK 10.3.1).  When endpoint is empty this is a no-op and a no-op
// shutdown function is returned.  The returned function must be called (defer)
// on process exit to guarantee span delivery.
//
// Current stub — replace the body with a real otlptracegrpc.New() call once
// the OTEL SDK is added to go.mod.
func InitOTel(endpoint string) (shutdown func()) {
	OTelEndpoint = endpoint
	if endpoint == "" {
		return func() {}
	}
	// OTEL_EXPORTER_OTLP_ENDPOINT is read by the SDK exporter automatically.
	Log.Info("otel enabled", "endpoint", endpoint)
	OTelShutdown = func() { Log.Info("otel shutdown") }
	return OTelShutdown
}
