package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jacobalberty/go-unifi/inform"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jacobalberty/beenfar/adapters/unifi"
	"github.com/jacobalberty/beenfar/api/adapter"
	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
)

// unifiDevice plays a UniFi device against the real server.
type unifiDevice struct {
	t   *testing.T
	f   apiFixture
	mac net.HardwareAddr
}

func (d unifiDevice) inform(key []byte, cfgversion string) (int, map[string]any) {
	d.t.Helper()
	flags := inform.FlagEncrypted | inform.FlagSnappy
	msg, _ := json.Marshal(map[string]any{"mac": d.mac.String(), "model": "U7PG2", "version": "4.0.66.10832", "cfgversion": cfgversion})
	p, err := inform.Seal(d.mac, flags, key, msg)
	if err != nil {
		d.t.Fatal(err)
	}
	wire, _ := p.MarshalBinary()
	req, _ := http.NewRequestWithContext(d.t.Context(), http.MethodPost, d.f.srv.URL+"/inform", bytes.NewReader(wire))
	resp, err := d.f.srv.Client().Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	rp, err := inform.Parse(body)
	if err != nil {
		d.t.Fatal(err)
	}
	plain, err := rp.Decode(key)
	if err != nil {
		d.t.Fatalf("reply is not sealed with the key the device used: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(plain, &out); err != nil {
		d.t.Fatal(err)
	}
	return resp.StatusCode, out
}

// newUnifiFixture runs the real UniFi adapter. Its spans go to the returned
// recorder, apart from the server's.
func newUnifiFixture(t *testing.T) (apiFixture, *tracetest.SpanRecorder) {
	t.Helper()
	adapterSpans := tracetest.NewSpanRecorder()
	ad, err := unifi.New(unifi.Options{Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(adapterSpans)).Tracer("test")})
	if err != nil {
		t.Fatal(err)
	}
	f := newAPIFixtureWith(t, []adapter.Adapter{ad})
	// Start runs in a goroutine; Forget on an unknown device is a probe that
	// only succeeds once it has.
	for range 500 {
		if ad.Forget(t.Context(), adapter.Device{ID: "probe"}) == nil {
			return f, adapterSpans
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("unifi adapter did not start")
	return f, nil
}

func authKeyOf(t *testing.T, reply map[string]any) ([]byte, string) {
	t.Helper()
	if reply["_type"] != "setparam" {
		t.Fatalf("reply = %v, want a setparam", reply)
	}
	cfg, err := inform.ParseConfig(reply["mgmt_cfg"].(string))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := cfg.Get(inform.KeyAuthKey)
	key, err := inform.ParseKey(v)
	if err != nil {
		t.Fatal(err)
	}
	version, _ := cfg.Get(inform.KeyCfgVersion)
	return key, version
}

// An unknown device gets 404 and becomes pending; adopting it through the
// API makes its next inform return an encrypted setparam with a new key; a
// lost reply is harmless; after the switch the old key is rejected.
func TestUnifiAdoptionEndToEnd(t *testing.T) {
	f, adapterSpans := newUnifiFixture(t)
	ctx := t.Context()
	devices := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))
	d := unifiDevice{t: t, f: f, mac: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x10, 0x01}}

	if status, _ := d.inform(inform.DefaultKey(), ""); status != http.StatusNotFound {
		t.Fatalf("unknown device: status = %d, want 404", status)
	}
	pending, err := devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	if err != nil || len(pending.GetDevices()) != 1 {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	p := pending.GetDevices()[0]
	if p.GetMac() != d.mac.String() || p.GetModel() != "U7PG2" || p.GetAdapter() != "unifi" || p.GetFirmware() != "4.0.66.10832" {
		t.Fatalf("pending device = %v", p)
	}

	if _, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: p.GetId()}}); err != nil {
		t.Fatal(err)
	}

	status, reply := d.inform(inform.DefaultKey(), "")
	if status != http.StatusOK {
		t.Fatalf("after adoption: status = %d", status)
	}
	newKey, version := authKeyOf(t, reply)

	// The reply is lost; the device asks again and gets the same key.
	_, again := d.inform(inform.DefaultKey(), "")
	if k, _ := authKeyOf(t, again); !bytes.Equal(k, newKey) {
		t.Fatal("the key changed between two replies")
	}

	if status, reply := d.inform(newKey, version); status != http.StatusOK || reply["_type"] != "noop" {
		t.Fatalf("with the new key: %d %v", status, reply)
	}
	if status, _ := d.inform(inform.DefaultKey(), ""); status != http.StatusNotFound {
		t.Fatalf("old key: status = %d, want 404", status)
	}

	// Each inform is a server span with the adapter's span directly below
	// it, carrying the device, the flags and the kind of reply.
	serverSpans := map[string]bool{}
	for _, sp := range f.spans.Ended() {
		if sp.Name() == "POST /inform" {
			serverSpans[sp.SpanContext().SpanID().String()] = true
		}
	}
	replies := map[string]int{}
	for _, sp := range adapterSpans.Ended() {
		if sp.Name() != "unifi.inform" {
			continue
		}
		if !serverSpans[sp.Parent().SpanID().String()] {
			t.Errorf("unifi.inform is not a child of a POST /inform span")
		}
		attrs := map[string]string{}
		for _, kv := range sp.Attributes() {
			attrs[string(kv.Key)] = kv.Value.String()
		}
		if attrs["device.mac"] != d.mac.String() || attrs["unifi.flags"] != "encrypted|snappy" {
			t.Errorf("attributes = %v", attrs)
		}
		replies[attrs["unifi.response"]]++
	}
	want := map[string]int{"not_found": 1, "setparam": 2, "noop": 1, "undecryptable": 1}
	for k, n := range want {
		if replies[k] != n {
			t.Errorf("responses = %v, want %v", replies, want)
			break
		}
	}
}
