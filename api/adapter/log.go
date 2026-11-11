package adapter

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

type logAttrsKey struct{}

// WithLogAttrs returns a context whose attributes are added to every log
// line written with it by a handler from NewLogHandler.
func WithLogAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(logAttrsKey{}).([]slog.Attr)
	all := make([]slog.Attr, 0, len(prev)+len(attrs))
	all = append(append(all, prev...), attrs...)
	return context.WithValue(ctx, logAttrsKey{}, all)
}

// NewLogHandler wraps next so that each record carries the attributes added
// to its context with WithLogAttrs and the trace_id and span_id of the span in
// its context. Log with the *Context methods (slog.InfoContext and friends)
// so the context reaches the handler.
func NewLogHandler(next slog.Handler) slog.Handler { return logHandler{next} }

type logHandler struct{ next slog.Handler }

func (h logHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h logHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs, ok := ctx.Value(logAttrsKey{}).([]slog.Attr); ok {
		r.AddAttrs(attrs...)
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.next.Handle(ctx, r)
}

func (h logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{h.next.WithAttrs(attrs)}
}

func (h logHandler) WithGroup(name string) slog.Handler { return logHandler{h.next.WithGroup(name)} }
