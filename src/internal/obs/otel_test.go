package obs_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/obs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
)

// fakeCollector is a minimal in-process OTLP/gRPC trace collector.
type fakeCollector struct {
	collectortrace.UnimplementedTraceServiceServer
	mu    sync.Mutex
	spans []*tracepb.Span
}

func (f *fakeCollector) Export(_ context.Context, req *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			f.spans = append(f.spans, ss.Spans...)
		}
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

func (f *fakeCollector) snapshot() []*tracepb.Span {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*tracepb.Span, len(f.spans))
	copy(out, f.spans)
	return out
}

// TestInitOTel_exports_connected_span_tree proves InitOTel wires a real
// OTLP/gRPC exporter: a parent and child span created via obs.Tracer arrive at
// a collector sharing one trace ID with a correct parent link (B2 smoke).
func TestInitOTel_exports_connected_span_tree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fc := &fakeCollector{}
	srv := grpc.NewServer()
	collectortrace.RegisterTraceServiceServer(srv, fc)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	shutdown := obs.InitOTel(ln.Addr().String())
	t.Cleanup(func() { obs.InitOTel("") }) // restore no-op tracer for other tests

	ctx, parent := obs.Tracer.Start(context.Background(), "staircase.run")
	_, child := obs.Tracer.Start(ctx, "staircase.yield")
	child.End()
	parent.End()
	shutdown() // flushes the batcher

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(fc.snapshot()) < 2 {
		time.Sleep(50 * time.Millisecond)
	}
	spans := fc.snapshot()
	require.Len(t, spans, 2, "both spans must reach the collector")

	byName := map[string]*tracepb.Span{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	run, yield := byName["staircase.run"], byName["staircase.yield"]
	require.NotNil(t, run)
	require.NotNil(t, yield)
	assert.Equal(t, run.TraceId, yield.TraceId, "spans must share one trace")
	assert.Equal(t, run.SpanId, yield.ParentSpanId, "yield must be a child of run")
}

// TestInitOTel_empty_endpoint_is_noop guards the disabled path.
func TestInitOTel_empty_endpoint_is_noop(t *testing.T) {
	shutdown := obs.InitOTel("")
	require.NotNil(t, shutdown)
	shutdown() // must not panic or block
	_, span := obs.Tracer.Start(context.Background(), "noop")
	span.End() // no-op tracer must accept spans silently
}
