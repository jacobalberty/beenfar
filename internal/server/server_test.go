package server_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/server"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/otelstore"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

type fixture struct {
	handler http.Handler
	store   store.Store
	auth    *auth.Service
	spans   *tracetest.SpanRecorder
}

// fixtureOption adjusts the server options; the auth service is ready.
type fixtureOption func(*server.Options)

// withAuthenticator replaces the default (tokens and session cookie).
func withAuthenticator(f func(*auth.Service) auth.Authenticator) fixtureOption {
	return func(o *server.Options) { o.Authenticator = f(o.Auth) }
}

func newFixture(t *testing.T, fopts ...fixtureOption) fixture {
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
	opts := server.Options{Store: st, Telemetry: tel, Auth: authSvc, Core: core.New(st, nil)}
	for _, f := range fopts {
		f(&opts)
	}
	h, err := server.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{handler: h, store: raw, auth: authSvc, spans: spans}
}

// informPacket builds an unencrypted inform packet for mac (12 hex digits).
func informPacket(t *testing.T, mac string) []byte {
	t.Helper()
	macBytes, err := hex.DecodeString(mac)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{}`)
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, int32(1414414933)) // "TNBU"
	_ = binary.Write(&b, binary.BigEndian, int32(0))          // packet version
	b.Write(macBytes)
	_ = binary.Write(&b, binary.BigEndian, int16(0)) // flags
	b.Write(make([]byte, 16))                        // IV
	_ = binary.Write(&b, binary.BigEndian, int32(1)) // data version
	_ = binary.Write(&b, binary.BigEndian, int32(len(payload)))
	b.Write(payload)
	return b.Bytes()
}

func post(h http.Handler, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/inform", bytes.NewReader(body)))
	return rec
}

func TestInformRecordsPendingDevice(t *testing.T) {
	f := newFixture(t)

	for range 2 { // a repeat inform must not create a second device
		if rec := post(f.handler, informPacket(t, "deadbeef0000")); rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	}
	devs, err := f.store.ListDevices(t.Context(), store.DeviceFilter{State: store.DeviceStatePending})
	if err != nil || len(devs) != 1 {
		t.Fatalf("pending = %+v, %v", devs, err)
	}
	if d := devs[0]; d.MAC != "de:ad:be:ef:00:00" || d.Adapter != "unifi" || d.IP == "" {
		t.Errorf("unexpected device %+v", d)
	}

	// Store spans are children of the request span and carry the device MAC.
	var serverSpan sdktrace.ReadOnlySpan
	var storeSpans []sdktrace.ReadOnlySpan
	for _, s := range f.spans.Ended() {
		switch s.Name() {
		case "POST /inform":
			serverSpan = s
		case "store.ReportDevice":
			storeSpans = append(storeSpans, s)
		}
	}
	if serverSpan == nil || len(storeSpans) == 0 {
		t.Fatalf("missing spans: %v", f.spans.Ended())
	}
	last := storeSpans[len(storeSpans)-1]
	if last.Parent().SpanID() != serverSpan.SpanContext().SpanID() {
		t.Errorf("store span is not a child of the request span")
	}
	hasMAC := false
	for _, kv := range last.Attributes() {
		if kv.Key == "device.mac" && kv.Value.AsString() == "deadbeef0000" {
			hasMAC = true
		}
	}
	if !hasMAC {
		t.Errorf("store span lacks device.mac: %v", last.Attributes())
	}
}

func TestInformAdoptedDeviceNotImplementedYet(t *testing.T) {
	f := newFixture(t)
	d, err := f.store.ReportDevice(t.Context(), store.DiscoveredDevice{MAC: "deadbeef0001", Adapter: "unifi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AdoptDevice(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	if rec := post(f.handler, informPacket(t, "deadbeef0001")); rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
	got, _ := f.store.GetDevice(t.Context(), d.ID)
	if got.State != store.DeviceStateAdopted {
		t.Errorf("adopted device reset to %s", got.State)
	}
}

func TestInformRejectsMalformed(t *testing.T) {
	f := newFixture(t)
	for name, body := range map[string][]byte{
		"empty":          nil,
		"short":          make([]byte, 39),
		"length overrun": append(informPacket(t, "deadbeef0002")[:36], 0x7f, 0, 0, 0),
	} {
		if rec := post(f.handler, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
	if devs, _ := f.store.ListDevices(t.Context(), store.DeviceFilter{}); len(devs) != 0 {
		t.Errorf("malformed packets created devices: %+v", devs)
	}
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
