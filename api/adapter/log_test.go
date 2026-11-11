package adapter_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/jacobalberty/beenfar/api/adapter"
)

func TestLogHandlerAddsContextAttrsAndTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(adapter.NewLogHandler(slog.NewJSONHandler(&buf, nil)))

	traceID, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	spanID, _ := trace.SpanIDFromHex("0102030405060708")
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}))
	ctx = adapter.WithLogAttrs(ctx, slog.String("adapter", "meraki"))
	ctx = adapter.WithLogAttrs(ctx, slog.String("device.mac", "de:ad:be:ef:00:01"))

	log.InfoContext(ctx, "hello")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"adapter": "meraki", "device.mac": "de:ad:be:ef:00:01",
		"trace_id": traceID.String(), "span_id": spanID.String(),
	} {
		if rec[k] != want {
			t.Errorf("%s = %v, want %s (%v)", k, rec[k], want, rec)
		}
	}

	// Without a span or attributes the line is left alone.
	buf.Reset()
	log.InfoContext(t.Context(), "plain")
	rec = nil
	_ = json.Unmarshal(buf.Bytes(), &rec)
	if _, ok := rec["trace_id"]; ok {
		t.Errorf("trace_id on a line without a span: %v", rec)
	}
}
