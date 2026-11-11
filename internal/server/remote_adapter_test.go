package server_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jacobalberty/beenfar/api/adapter"
	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
	"github.com/jacobalberty/beenfar/internal/store"
)

// stubRemote is an external adapter that records what it is asked to do.
type stubRemote struct {
	host     chan adapter.Host
	mu       sync.Mutex
	adopted  []adapter.Device
	forgot   []adapter.Device
	adoptErr error
}

func (s *stubRemote) Name() string { return "stubremote" }

func (s *stubRemote) Start(ctx context.Context, h adapter.Host) error {
	s.host <- h
	<-ctx.Done()
	return nil
}

func (s *stubRemote) Adopt(ctx context.Context, d adapter.Device) error {
	// Calls made while carrying out a command belong to the command's trace.
	h := <-s.host
	s.host <- h
	if err := h.SetDeviceSecret(ctx, d.ID, "current", []byte("k")); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adopted = append(s.adopted, d)
	return s.adoptErr
}

func (s *stubRemote) Forget(_ context.Context, d adapter.Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forgot = append(s.forgot, d)
	return nil
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunRemote(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	token := adapterToken(t, f, "stubremote")
	devices := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))

	adapterSpans := tracetest.NewSpanRecorder()
	stub := &stubRemote{host: make(chan adapter.Host, 1)}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- adapter.RunRemote(runCtx, f.srv.URL, token, stub,
			adapter.WithHTTPClient(f.srv.Client()),
			adapter.WithVersion("9.9"),
			adapter.WithTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(adapterSpans))),
			adapter.WithPropagator(propagation.TraceContext{}),
			adapter.WithoutSlogHandler(),
		)
	}()
	var host adapter.Host
	select {
	case host = <-stub.host:
		stub.host <- host
	case <-time.After(5 * time.Second):
		t.Fatal("adapter was not started")
	}

	// The adapter reports a device through its Host.
	d, err := host.ReportDevice(ctx, adapter.Discovered{MAC: "de:ad:be:ef:00:09", Model: "X1"})
	if err != nil || d.State != adapter.DevicePending {
		t.Fatalf("ReportDevice = %+v, %v", d, err)
	}
	if got, err := host.DeviceByMAC(ctx, "deadbeef0009"); err != nil || got.ID != d.ID {
		t.Fatalf("DeviceByMAC = %+v, %v", got, err)
	}
	if _, err := host.DeviceByMAC(ctx, "deadbeef0010"); !errors.Is(err, adapter.ErrNotFound) {
		t.Fatalf("DeviceByMAC unknown = %v", err)
	}
	if _, err := host.DeviceSecret(ctx, d.ID, "nope"); !errors.Is(err, adapter.ErrNoSecret) {
		t.Fatalf("DeviceSecret missing = %v", err)
	}

	// Adopting the device through the public API reaches the adapter.
	adopted, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: d.ID}})
	if err != nil || adopted.GetDevice().GetState() != configv1.DeviceState_DEVICE_STATE_ADOPTED {
		t.Fatalf("AdoptDevice = %v, %v", adopted, err)
	}
	if len(stub.adopted) != 1 || stub.adopted[0].ID != d.ID || stub.adopted[0].MAC != "de:ad:be:ef:00:09" {
		t.Fatalf("adopted = %+v", stub.adopted)
	}
	if secret, err := host.DeviceSecret(ctx, d.ID, "current"); err != nil || string(secret) != "k" {
		t.Fatalf("secret stored during Adopt = %q, %v", secret, err)
	}

	// The trace continues across the boundary: the adapter's span for the
	// command and the server span of the call it made while handling it are
	// in the trace of the AdoptDevice request.
	find := func(rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
		var out []sdktrace.ReadOnlySpan
		for _, s := range rec.Ended() {
			if s.Name() == name {
				out = append(out, s)
			}
		}
		return out
	}
	rpc := find(f.spans, "beenfar.config.v1.DeviceService/AdoptDevice")
	cmd := find(adapterSpans, "adapter.Adopt")
	inner := find(f.spans, "beenfar.adapter.v1.AdapterHostService/SetDeviceSecret")
	if len(rpc) != 1 || len(cmd) != 1 || len(inner) < 1 {
		t.Fatalf("spans: AdoptDevice=%d adapter.Adopt=%d SetDeviceSecret=%d", len(rpc), len(cmd), len(inner))
	}
	traceID := rpc[0].SpanContext().TraceID()
	if cmd[0].SpanContext().TraceID() != traceID {
		t.Errorf("adapter.Adopt is in trace %s, want %s", cmd[0].SpanContext().TraceID(), traceID)
	}
	if cmd[0].Parent().SpanID() != rpc[0].SpanContext().SpanID() && !cmd[0].Parent().IsRemote() {
		t.Errorf("adapter.Adopt parent = %v", cmd[0].Parent())
	}
	if inner[0].SpanContext().TraceID() != traceID {
		t.Errorf("SetDeviceSecret is in trace %s, want %s", inner[0].SpanContext().TraceID(), traceID)
	}

	// A failing Adopt fails the API call and leaves the device pending.
	d2, err := host.ReportDevice(ctx, adapter.Discovered{MAC: "de:ad:be:ef:00:0a"})
	if err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	stub.adoptErr = errors.New("cannot reach device")
	stub.mu.Unlock()
	if _, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: d2.ID}}); err == nil {
		t.Fatal("adopt succeeded although the adapter failed")
	}
	if got, _ := f.store.GetDevice(ctx, d2.ID); got.State != store.DeviceStatePending {
		t.Fatalf("state after failed adopt = %s", got.State)
	}

	// Forget reaches the adapter.
	if _, err := devices.ForgetDevice(ctx, &configv1.ForgetDeviceRequest{Id: d.ID}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "forget", func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return len(stub.forgot) == 1 })

	// Stopping the adapter ends RunRemote cleanly and the controller sees it go.
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunRemote = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunRemote did not return")
	}
	waitFor(t, "adapter to disconnect", func() bool {
		for _, a := range f.registry.ListAdapters(ctx) {
			if a.Name == "stubremote" {
				return !a.Healthy
			}
		}
		return false
	})
}

func TestRunRemoteRefusedToken(t *testing.T) {
	f := newAPIFixture(t)
	stub := &stubRemote{host: make(chan adapter.Host, 1)}
	err := adapter.RunRemote(t.Context(), f.srv.URL, "bfa_aaaaaaaaaaaa_wrong", stub,
		adapter.WithHTTPClient(f.srv.Client()), adapter.WithoutSlogHandler())
	if err == nil {
		t.Fatal("RunRemote accepted a bad token")
	}
}
