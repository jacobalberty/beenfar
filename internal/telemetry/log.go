package telemetry

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// LogConfig controls the local log output.
type LogConfig struct {
	Writer io.Writer
	Format string // "json" (default) or "text"
	Level  slog.Leveler
}

// Logger returns a slog.Logger that
//   - writes to cfg.Writer with trace_id/span_id taken from the context, and
//   - when OTLP log export is enabled, also sends each record through the
//     otelslog bridge, which links it to the active span.
//
// Both outputs include the attributes added to the context with WithAttrs.
// Use the *Context logging methods so the context reaches the handler.
func (t *Telemetry) Logger(cfg LogConfig) *slog.Logger {
	level := cfg.Level
	if level == nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var local slog.Handler
	if cfg.Format == "text" {
		local = slog.NewTextHandler(cfg.Writer, opts)
	} else {
		local = slog.NewJSONHandler(cfg.Writer, opts)
	}
	handlers := []slog.Handler{&contextHandler{next: local, level: level, traceIDs: true}}
	if t.LoggerProvider != nil {
		bridge := otelslog.NewHandler(ScopeName, otelslog.WithLoggerProvider(t.LoggerProvider))
		handlers = append(handlers, &contextHandler{next: bridge, level: level})
	}
	if len(handlers) == 1 {
		return slog.New(handlers[0])
	}
	return slog.New(slog.NewMultiHandler(handlers...))
}

// contextHandler adds context-scoped attributes (and optionally the trace and
// span IDs) to every record.
type contextHandler struct {
	next     slog.Handler
	level    slog.Leveler
	traceIDs bool
}

func (h *contextHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level.Level() && h.next.Enabled(ctx, l)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := AttrsFrom(ctx)
	sc := trace.SpanContextFromContext(ctx)
	if len(attrs) > 0 || (h.traceIDs && sc.IsValid()) {
		r = r.Clone()
		if h.traceIDs && sc.IsValid() {
			r.AddAttrs(
				slog.String("trace_id", sc.TraceID().String()),
				slog.String("span_id", sc.SpanID().String()),
			)
		}
		r.AddAttrs(attrs...)
	}
	return h.next.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs), level: h.level, traceIDs: h.traceIDs}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name), level: h.level, traceIDs: h.traceIDs}
}

type attrsKey struct{}

// WithAttrs returns a context carrying attrs in addition to any already
// present. Every log record written with the returned context, and every span
// started with StartSpan, carries them. They are also set on the span that is
// active in ctx.
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}
	prev := AttrsFrom(ctx)
	merged := make([]slog.Attr, 0, len(prev)+len(attrs))
	merged = append(merged, prev...)
	merged = append(merged, attrs...)
	trace.SpanFromContext(ctx).SetAttributes(otelAttrs(attrs)...)
	return context.WithValue(ctx, attrsKey{}, merged)
}

// AttrsFrom returns the attributes added to ctx with WithAttrs.
func AttrsFrom(ctx context.Context) []slog.Attr {
	attrs, _ := ctx.Value(attrsKey{}).([]slog.Attr)
	return attrs
}

// StartSpan starts a span that also carries the context's WithAttrs attributes.
func StartSpan(ctx context.Context, tracer trace.Tracer, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if attrs := AttrsFrom(ctx); len(attrs) > 0 {
		opts = append(opts, trace.WithAttributes(otelAttrs(attrs)...))
	}
	return tracer.Start(ctx, name, opts...)
}

func otelAttrs(attrs []slog.Attr) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		v := a.Value.Resolve()
		switch v.Kind() {
		case slog.KindString:
			out = append(out, attribute.String(a.Key, v.String()))
		case slog.KindInt64:
			out = append(out, attribute.Int64(a.Key, v.Int64()))
		case slog.KindUint64:
			out = append(out, attribute.Int64(a.Key, int64(v.Uint64()))) //nolint:gosec // best effort
		case slog.KindBool:
			out = append(out, attribute.Bool(a.Key, v.Bool()))
		case slog.KindFloat64:
			out = append(out, attribute.Float64(a.Key, v.Float64()))
		default:
			out = append(out, attribute.String(a.Key, v.String()))
		}
	}
	return out
}
