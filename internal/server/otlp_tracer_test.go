package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// #1163 v0.67 traces half: per-tool-call OTLP span emission. Tests use
// the SDK's tracetest in-memory recorder so we exercise the real span
// machinery (semconv attrs, status codes, span kind) without binding to
// a live collector.

// installRecorder swaps the server's tracer for an in-memory recorder
// and returns the recorder so tests can introspect emitted spans.
// Returns a teardown closure for parallel-test safety.
func installRecorder(t *testing.T, s *Server) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := s.tracer
	s.tracer = &pincherTracer{
		provider: tp,
		tracer:   tp.Tracer(otlpTraceServiceName),
	}
	t.Cleanup(func() { s.tracer = prev })
	return rec
}

// Positive: a tool call emits exactly one span with the standard
// attributes (rpc.system, rpc.method, complexity_tier, request_id).
func TestWithTracing_EmitsSpanPerCall(t *testing.T) {
	srv, _, _ := newTestServer(t)
	rec := installRecorder(t, srv)

	// Run a single search call. The wrapping happens at addTool time
	// (before the test ran), so the registered handler already carries
	// withTracing → withRequestID. Drive it through the MCP server's
	// handler map.
	handler := srv.handlers["search"]
	if handler == nil {
		t.Fatal("search handler not registered")
	}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: "search", Arguments: []byte(`{"q":"db"}`),
	}}
	if _, err := handler(context.Background(), req); err != nil {
		t.Fatalf("handler: %v", err)
	}

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span; got %d", len(spans))
	}
	sp := spans[0]
	if sp.Name() != "pincher.tool.search" {
		t.Errorf("span name = %q; want pincher.tool.search", sp.Name())
	}
	attrs := attrMap(sp.Attributes())
	if attrs["rpc.system"] != "mcp" {
		t.Errorf("rpc.system = %v; want mcp", attrs["rpc.system"])
	}
	if attrs["rpc.method"] != "search" {
		t.Errorf("rpc.method = %v; want search", attrs["rpc.method"])
	}
	if attrs["pincher.complexity_tier"] != "lite" {
		t.Errorf("complexity_tier = %v; want lite", attrs["pincher.complexity_tier"])
	}
	if _, ok := attrs["pincher.request_id"]; !ok {
		t.Errorf("expected pincher.request_id attribute; got %v", attrs)
	}
}

// Cross-check: capability advertisement only flips when the OTLP
// endpoint init succeeded. Default (no env var) → no advertisement
// even though withTracing still runs against a no-op tracer.
func TestCapabilities_OmitsTracesOTLPWhenNotConfigured(t *testing.T) {
	srv, _, _ := newTestServer(t)
	for _, c := range computeCapabilities(srv) {
		if c == "traces_otlp" {
			t.Errorf("traces_otlp advertised but no OTLP endpoint configured")
		}
	}
}

// Control: even with the noop tracer (default), the wrapper must not
// drop the response payload — instrumentation must never break the
// happy path.
func TestWithTracing_PreservesResponseUnderNoop(t *testing.T) {
	srv, _, _ := newTestServer(t)
	handler := srv.handlers["health"]
	if handler == nil {
		t.Fatal("health handler not registered")
	}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: "health", Arguments: []byte(`{}`),
	}}
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("health handler: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected response content; got empty")
	}
}

// Negative: an HTTP-routed tool call inherits the request_id and the
// span carries the inbound X-Request-ID rather than a freshly minted
// one. Combined with the request_id attr assertion above this proves
// the trace can be correlated end-to-end with a router's request ID.
func TestWithTracing_HTTPInheritsRequestID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	rec := installRecorder(t, srv)

	req := httptest.NewRequest("POST", "/v1/search", strings.NewReader(`{"q":"db"}`))
	req.Header.Set("X-Request-ID", "router-trace-id-123")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	// The search-call span should carry the supplied request_id.
	var found bool
	for _, sp := range spans {
		if sp.Name() == "pincher.tool.search" {
			attrs := attrMap(sp.Attributes())
			if attrs["pincher.request_id"] == "router-trace-id-123" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected span carrying inbound X-Request-ID router-trace-id-123; got %d spans", len(spans))
	}
}

func attrMap(kvs []attribute.KeyValue) map[string]any {
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		out[string(kv.Key)] = kv.Value.AsInterface()
	}
	return out
}
