// Package otelstore decorates any store.Store with an OpenTelemetry span and a
// duration histogram per operation.
package otelstore

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

// Store wraps another store.Store.
type Store struct {
	next   store.Store
	system string
	tracer trace.Tracer
	dur    metric.Float64Histogram
}

var _ store.Store = (*Store)(nil)

// New wraps next. system names the database ("sqlite") for the
// db.system.name attribute.
func New(next store.Store, system string, t *telemetry.Telemetry) (*Store, error) {
	dur, err := t.Meter().Float64Histogram("beenfar.store.operation.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of store operations."),
	)
	if err != nil {
		return nil, err
	}
	return &Store{next: next, system: system, tracer: t.Tracer(), dur: dur}, nil
}

func (s *Store) wrap(next store.Store) *Store {
	return &Store{next: next, system: s.system, tracer: s.tracer, dur: s.dur}
}

func run[T any](ctx context.Context, s *Store, op string, fn func(context.Context) (T, error)) (T, error) {
	attrs := []attribute.KeyValue{
		attribute.String("db.system.name", s.system),
		attribute.String("db.operation.name", op),
	}
	ctx, span := telemetry.StartSpan(ctx, s.tracer, "store."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	defer span.End()
	start := time.Now()

	v, err := fn(ctx)

	if errType := errorType(err); errType != "" {
		attrs = append(attrs, attribute.String("error.type", errType))
		span.SetAttributes(attribute.String("error.type", errType))
		if errType == "error" {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}
	s.dur.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
	return v, err
}

// errorType classifies err. Expected outcomes (not found, conflict, invalid)
// are labelled but do not mark the span as failed.
func errorType(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, store.ErrNotFound):
		return "not_found"
	case errors.Is(err, store.ErrConflict):
		return "conflict"
	case errors.Is(err, store.ErrInvalid):
		return "invalid"
	default:
		return "error"
	}
}

func run0(ctx context.Context, s *Store, op string, fn func(context.Context) error) error {
	_, err := run(ctx, s, op, func(ctx context.Context) (struct{}, error) { return struct{}{}, fn(ctx) })
	return err
}

func (s *Store) WithTx(ctx context.Context, fn func(store.Store) error) error {
	return run0(ctx, s, "WithTx", func(ctx context.Context) error {
		return s.next.WithTx(ctx, func(tx store.Store) error { return fn(s.wrap(tx)) })
	})
}

func (s *Store) Ping(ctx context.Context) error { return s.next.Ping(ctx) }

func (s *Store) Close() error { return s.next.Close() }

func (s *Store) ReportDevice(ctx context.Context, d store.DiscoveredDevice) (store.Device, error) {
	return run(ctx, s, "ReportDevice", func(ctx context.Context) (store.Device, error) { return s.next.ReportDevice(ctx, d) })
}

func (s *Store) GetDevice(ctx context.Context, id string) (store.Device, error) {
	return run(ctx, s, "GetDevice", func(ctx context.Context) (store.Device, error) { return s.next.GetDevice(ctx, id) })
}

func (s *Store) GetDeviceByMAC(ctx context.Context, mac string) (store.Device, error) {
	return run(ctx, s, "GetDeviceByMAC", func(ctx context.Context) (store.Device, error) { return s.next.GetDeviceByMAC(ctx, mac) })
}

func (s *Store) ListDevices(ctx context.Context, f store.DeviceFilter) ([]store.Device, error) {
	return run(ctx, s, "ListDevices", func(ctx context.Context) ([]store.Device, error) { return s.next.ListDevices(ctx, f) })
}

func (s *Store) AdoptDevice(ctx context.Context, id string) (store.Device, error) {
	return run(ctx, s, "AdoptDevice", func(ctx context.Context) (store.Device, error) { return s.next.AdoptDevice(ctx, id) })
}

func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	return run0(ctx, s, "DeleteDevice", func(ctx context.Context) error { return s.next.DeleteDevice(ctx, id) })
}

func (s *Store) CreateWifiNetwork(ctx context.Context, w store.WifiNetwork) (store.WifiNetwork, error) {
	return run(ctx, s, "CreateWifiNetwork", func(ctx context.Context) (store.WifiNetwork, error) { return s.next.CreateWifiNetwork(ctx, w) })
}

func (s *Store) GetWifiNetwork(ctx context.Context, id string) (store.WifiNetwork, error) {
	return run(ctx, s, "GetWifiNetwork", func(ctx context.Context) (store.WifiNetwork, error) { return s.next.GetWifiNetwork(ctx, id) })
}

func (s *Store) ListWifiNetworks(ctx context.Context) ([]store.WifiNetwork, error) {
	return run(ctx, s, "ListWifiNetworks", func(ctx context.Context) ([]store.WifiNetwork, error) { return s.next.ListWifiNetworks(ctx) })
}

func (s *Store) UpdateWifiNetwork(ctx context.Context, w store.WifiNetwork) (store.WifiNetwork, error) {
	return run(ctx, s, "UpdateWifiNetwork", func(ctx context.Context) (store.WifiNetwork, error) { return s.next.UpdateWifiNetwork(ctx, w) })
}

func (s *Store) DeleteWifiNetwork(ctx context.Context, id string, revision int64) error {
	return run0(ctx, s, "DeleteWifiNetwork", func(ctx context.Context) error { return s.next.DeleteWifiNetwork(ctx, id, revision) })
}
