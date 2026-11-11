// Package telemetry wires OpenTelemetry traces, metrics and logs for beenfar.
//
// Everything is configured through the standard OTEL_* environment variables.
// A signal is exported only when an OTLP endpoint is configured for it
// (OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT) and
// neither OTEL_SDK_DISABLED=true nor OTEL_<SIGNAL>_EXPORTER=none is set.
// Otherwise the signal uses a no-op provider and costs nothing.
package telemetry

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// ScopeName is the instrumentation scope used for beenfar's own telemetry.
const ScopeName = "github.com/jacobalberty/beenfar"

// Config identifies the service in exported telemetry. OTEL_SERVICE_NAME and
// OTEL_RESOURCE_ATTRIBUTES override these values.
type Config struct {
	ServiceName    string
	ServiceVersion string
}

// Telemetry holds the providers used by the rest of the program. The zero
// value is not usable; build one with Setup, or fill the fields directly in
// tests (a nil LoggerProvider disables OTLP log export).
type Telemetry struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	LoggerProvider otellog.LoggerProvider
	Propagator     propagation.TextMapPropagator

	shutdown []func(context.Context) error
}

// Setup builds the providers from the environment and installs them as the
// OpenTelemetry globals.
func Setup(ctx context.Context, cfg Config) (*Telemetry, error) {
	t := &Telemetry{
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{},
		),
	}
	otel.SetTextMapPropagator(t.Propagator)

	traces, metrics, logs := signalEnabled("TRACES"), signalEnabled("METRICS"), signalEnabled("LOGS")
	if !traces && !metrics && !logs {
		return t, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.version", cfg.ServiceVersion),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, err
	}

	if traces {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, errors.Join(err, t.Shutdown(ctx))
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
		t.TracerProvider = tp
		t.shutdown = append(t.shutdown, tp.Shutdown)
		otel.SetTracerProvider(tp)
	}
	if metrics {
		exp, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return nil, errors.Join(err, t.Shutdown(ctx))
		}
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)),
			sdkmetric.WithResource(res),
		)
		t.MeterProvider = mp
		t.shutdown = append(t.shutdown, mp.Shutdown)
		otel.SetMeterProvider(mp)
	}
	if logs {
		exp, err := otlploghttp.New(ctx)
		if err != nil {
			return nil, errors.Join(err, t.Shutdown(ctx))
		}
		lp := sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
			sdklog.WithResource(res),
		)
		t.LoggerProvider = lp
		t.shutdown = append(t.shutdown, lp.Shutdown)
		otel.SetLoggerProvider(lp)
	}
	return t, nil
}

// Shutdown flushes and stops every exporting provider.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, v := range slices.Backward(t.shutdown) {
		errs = append(errs, v(ctx))
	}
	t.shutdown = nil
	return errors.Join(errs...)
}

// Tracer returns beenfar's tracer.
func (t *Telemetry) Tracer() trace.Tracer { return t.TracerProvider.Tracer(ScopeName) }

// Meter returns beenfar's meter.
func (t *Telemetry) Meter() metric.Meter { return t.MeterProvider.Meter(ScopeName) }

func signalEnabled(signal string) bool {
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") {
		return false
	}
	if strings.EqualFold(os.Getenv("OTEL_"+signal+"_EXPORTER"), "none") {
		return false
	}
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") != ""
}
