package telemetry

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const requestIDHeader = "X-Request-Id"

// Middleware traces and measures every request, using plain net/http.
//
// It continues an incoming W3C trace, starts a server span, assigns a request
// ID (honouring a sane incoming X-Request-Id), puts request_id into the
// context for logging, and records http.server.request.duration. When the
// wrapped handler is an http.ServeMux, the span is named after the matched
// route pattern.
func (t *Telemetry) Middleware(next http.Handler) http.Handler {
	tracer := t.Tracer()
	duration, err := t.Meter().Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of HTTP server requests."),
	)
	if err != nil {
		otelError(err)
	}
	prop := t.TextMapPropagator()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		reqID := r.Header.Get(requestIDHeader)
		if !validRequestID(reqID) {
			reqID = strings.ToLower(rand.Text())
		}
		w.Header().Set(requestIDHeader, reqID)

		ctx, span := tracer.Start(ctx, r.Method,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
				attribute.String("client.address", r.RemoteAddr),
				attribute.String("user_agent.original", r.UserAgent()),
			),
		)
		defer span.End()
		ctx = WithAttrs(ctx, slog.String("request_id", reqID))

		rec := &statusRecorder{ResponseWriter: w}
		req := r.WithContext(ctx)
		next.ServeHTTP(rec, req)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		route := routeOf(req.Pattern)
		attrs := []attribute.KeyValue{
			attribute.String("http.request.method", r.Method),
			attribute.Int("http.response.status_code", status),
		}
		if route != "" {
			attrs = append(attrs, attribute.String("http.route", route))
			span.SetName(r.Method + " " + route)
		}
		span.SetAttributes(attrs...)
		if status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
		if duration != nil {
			duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
		}
		slog.DebugContext(ctx, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

// routeOf strips the optional method and host from a ServeMux pattern.
func routeOf(pattern string) string {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		pattern = strings.TrimLeft(pattern[i+1:], " \t")
	}
	if i := strings.IndexByte(pattern, '/'); i > 0 {
		pattern = pattern[i:]
	}
	return pattern
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c <= ' ' || c > '~' {
			return false
		}
	}
	return true
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Flush supports streaming handlers that type-assert http.Flusher.
func (s *statusRecorder) Flush() {
	_ = http.NewResponseController(s.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func otelError(err error) {
	// Instrument creation only fails on invalid names; surface it loudly in
	// tests and logs rather than panicking in production.
	slog.Default().Error("telemetry instrument", slog.Any("error", err)) //nolint:sloglint // no request context here
}
