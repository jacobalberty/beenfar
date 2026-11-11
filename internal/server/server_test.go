package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jacobalberty/beenfar/api/adapter"
	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/server"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/otelstore"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

type fixture struct {
	handler  http.Handler
	store    store.Store
	auth     *auth.Service
	spans    *tracetest.SpanRecorder
	registry *adapterhost.Registry
}

// fixtureOption adjusts the server options; the auth service is ready.
type fixtureOption func(*server.Options)

// withAuthenticator replaces the default (tokens and session cookie).
func withAuthenticator(f func(*auth.Service) auth.Authenticator) fixtureOption {
	return func(o *server.Options) { o.Authenticator = f(o.Auth) }
}

func newFixture(t *testing.T, fopts ...fixtureOption) fixture {
	t.Helper()
	return newFixtureWith(t, []adapter.Adapter{stubAdapter{}}, fopts...)
}

// newFixtureWith is newFixture with the given built-in adapters running.
func newFixtureWith(t *testing.T, adapters []adapter.Adapter, fopts ...fixtureOption) fixture {
	t.Helper()
	spans := tracetest.NewSpanRecorder()
	tel := &telemetry.Telemetry{
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
		MeterProvider:  sdkmetric.NewMeterProvider(),
		Propagator:     propagation.TraceContext{},
	}
	raw, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "beenfar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st, err := otelstore.New(raw, "sqlite", tel)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.New(st, auth.WithIterations(1000)) // fast hashes for tests
	reg, err := adapterhost.NewRegistry(adapterhost.Options{Store: st, Propagator: tel.TextMapPropagator()})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range adapters {
		if err := reg.AddBuiltin(a); err != nil {
			t.Fatal(err)
		}
	}
	reg.Start(t.Context())
	opts := server.Options{Store: st, Telemetry: tel, Auth: authSvc, Core: core.New(st, reg), AdapterHost: adapterhost.NewService(reg, st), AdapterHTTP: reg.HTTPHandlers()}
	for _, f := range fopts {
		f(&opts)
	}
	h, err := server.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{handler: h, store: raw, auth: authSvc, spans: spans, registry: reg}
}

func TestHealthz(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/device", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("old jsonapi route still served: %d", rec.Code)
	}
}

// stubAdapter is a built-in adapter named "unifi" that accepts every command.
type stubAdapter struct{}

func (stubAdapter) Name() string { return "unifi" }

func (stubAdapter) Start(ctx context.Context, _ adapter.Host) error {
	<-ctx.Done()
	return ctx.Err()
}

func (stubAdapter) Adopt(context.Context, adapter.Device) error  { return nil }
func (stubAdapter) Forget(context.Context, adapter.Device) error { return nil }
