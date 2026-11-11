package telemetry_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jacobalberty/beenfar/internal/telemetry"
)

type harness struct {
	tel    *telemetry.Telemetry
	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
}

func newHarness() harness {
	spans := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	return harness{
		tel: &telemetry.Telemetry{
			TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
			MeterProvider:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
			Propagator:     propagation.TraceContext{},
		},
		spans:  spans,
		reader: reader,
	}
}

func attr(kvs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, kv := range kvs {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestMiddlewareSpanMetricsAndLogs(t *testing.T) {
	h := newHarness()
	var logs bytes.Buffer
	logger := h.tel.Logger(telemetry.LogConfig{Writer: &logs, Level: slog.LevelInfo})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /things/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx := telemetry.WithAttrs(r.Context(), slog.String("thing", r.PathValue("id")))
		logger.InfoContext(ctx, "handled")
		w.WriteHeader(http.StatusTeapot)
	})
	srv := h.tel.Middleware(mux)

	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req := httptest.NewRequest(http.MethodPost, "/things/42", nil)
	req.Header.Set("traceparent", parent)
	req.Header.Set("X-Request-Id", "req-123")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot || rec.Header().Get("X-Request-Id") != "req-123" {
		t.Fatalf("status %d, request id %q", rec.Code, rec.Header().Get("X-Request-Id"))
	}

	ended := h.spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("got %d spans, want 1", len(ended))
	}
	span := ended[0]
	if span.Name() != "POST /things/{id}" {
		t.Errorf("span name = %q", span.Name())
	}
	if got := span.SpanContext().TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("incoming trace not continued: %s", got)
	}
	if v, _ := attr(span.Attributes(), "http.response.status_code"); v.AsInt64() != http.StatusTeapot {
		t.Errorf("status attribute = %v", v)
	}
	if v, _ := attr(span.Attributes(), "thing"); v.AsString() != "42" {
		t.Errorf("WithAttrs did not reach the active span: %v", span.Attributes())
	}

	var line map[string]any
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("log line %q: %v", logs.String(), err)
	}
	if line["trace_id"] != span.SpanContext().TraceID().String() || line["span_id"] != span.SpanContext().SpanID().String() {
		t.Errorf("log not correlated with span: %v", line)
	}
	if line["request_id"] != "req-123" || line["thing"] != "42" {
		t.Errorf("context attributes missing from log: %v", line)
	}

	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			hist := m.Data.(metricdata.Histogram[float64])
			for _, dp := range hist.DataPoints {
				if v, ok := dp.Attributes.Value("http.route"); ok && v.AsString() == "/things/{id}" && dp.Count == 1 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("no http.server.request.duration point for the route: %+v", rm)
	}
}

func TestMiddlewareRejectsBadRequestID(t *testing.T) {
	h := newHarness()
	srv := h.tel.Middleware(http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "bad\nid")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if id := rec.Header().Get("X-Request-Id"); id == "" || id == "bad\nid" {
		t.Errorf("request id = %q, want a generated one", id)
	}
}

func TestSetupWithoutEndpointIsNoop(t *testing.T) {
	for _, k := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
	} {
		t.Setenv(k, "")
	}
	tel, err := telemetry.Setup(t.Context(), telemetry.Config{ServiceName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if tel.LoggerProvider != nil {
		t.Error("log export enabled without an endpoint")
	}
	if err := tel.Shutdown(t.Context()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestSetupDisabled(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	tel, err := telemetry.Setup(t.Context(), telemetry.Config{ServiceName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if tel.LoggerProvider != nil {
		t.Error("OTEL_SDK_DISABLED ignored")
	}
}
