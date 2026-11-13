package server_test

import (
	"bytes"
	"connectrpc.com/connect/v2"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
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
func newUnifiFixture(t *testing.T, tweak ...func(*unifi.Options)) (apiFixture, *tracetest.SpanRecorder) {
	t.Helper()
	adapterSpans := tracetest.NewSpanRecorder()
	opts := unifi.Options{Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(adapterSpans)).Tracer("test")}
	for _, f := range tweak {
		f(&opts)
	}
	ad, err := unifi.New(opts)
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

// A reset device is shown as the same adopted device awaiting re-adoption,
// not as a new one; accepting is the call that adopts a new device.
func TestUnifiReadoptionThroughTheAPI(t *testing.T) {
	const window = 300 * time.Millisecond
	f, _ := newUnifiFixture(t, func(o *unifi.Options) { o.ReadoptAfter = window })
	ctx := t.Context()
	devices := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))
	d := unifiDevice{t: t, f: f, mac: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x40, 0x01}}

	d.inform(inform.DefaultKey(), "")
	pending, err := devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	if err != nil || len(pending.GetDevices()) != 1 {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	id := pending.GetDevices()[0].GetId()
	adopt := func() (*configv1.AdoptDeviceResponse, error) {
		return devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: id}})
	}
	if _, err := adopt(); err != nil {
		t.Fatal(err)
	}
	_, reply := d.inform(inform.DefaultKey(), "")
	key, version := authKeyOf(t, reply)
	d.inform(key, version)

	// A healthy adopted device has nothing to accept.
	_, err = adopt()
	wantCode(t, err, connect.CodeFailedPrecondition)

	// It is reset and comes back on the default key after being silent.
	time.Sleep(window + 100*time.Millisecond)
	if status, _ := d.inform(inform.DefaultKey(), ""); status != http.StatusNotFound {
		t.Fatalf("reset device: status = %d, want 404", status)
	}
	got, err := devices.GetDevice(ctx, &configv1.GetDeviceRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if g := got.GetDevice(); g.GetState() != configv1.DeviceState_DEVICE_STATE_ADOPTED || g.GetReadoption() != configv1.ReadoptionState_READOPTION_STATE_AWAITING_ACCEPTANCE || g.GetLocked() {
		t.Fatalf("device = %v", g)
	}
	// It is not listed among the new devices: it is not a new device.
	pending, err = devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	if err != nil || len(pending.GetDevices()) != 0 {
		t.Fatalf("a reset device is listed as pending: %v, %v", pending, err)
	}

	// Accepting re-adopts it.
	accepted, err := adopt()
	if err != nil || accepted.GetDevice().GetReadoption() != configv1.ReadoptionState_READOPTION_STATE_IN_PROGRESS {
		t.Fatalf("accept = %v, %v", accepted, err)
	}
	status, reply := d.inform(inform.DefaultKey(), "")
	if status != http.StatusOK {
		t.Fatalf("accepted device: status = %d", status)
	}
	key2, version := authKeyOf(t, reply)
	if bytes.Equal(key2, key) {
		t.Fatal("the key did not change")
	}
	if status, reply := d.inform(key2, version); status != http.StatusOK || reply["_type"] != "noop" {
		t.Fatalf("on the new key: %d %v", status, reply)
	}
	got, err = devices.GetDevice(ctx, &configv1.GetDeviceRequest{Id: id})
	if err != nil || got.GetDevice().GetReadoption() != configv1.ReadoptionState_READOPTION_STATE_UNSPECIFIED || got.GetDevice().GetState() != configv1.DeviceState_DEVICE_STATE_ADOPTED {
		t.Fatalf("device = %v, %v", got, err)
	}
}

// Forgetting an adopted device through the API resets it: the device stays
// listed as forgetting until it has reset, is told to reset on its next inform,
// is dropped once it speaks the default key, and comes back as a new, pending
// device that can be adopted again.
func TestForgetDeviceResetsTheDevice(t *testing.T) {
	f, _ := newUnifiFixture(t)
	ctx := t.Context()
	devices := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))
	d := unifiDevice{t: t, f: f, mac: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x30, 0x01}}

	d.inform(inform.DefaultKey(), "")
	pending, err := devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	if err != nil || len(pending.GetDevices()) != 1 {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	id := pending.GetDevices()[0].GetId()
	if _, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: id}}); err != nil {
		t.Fatal(err)
	}
	_, reply := d.inform(inform.DefaultKey(), "")
	key, version := authKeyOf(t, reply)
	if status, reply := d.inform(key, version); status != http.StatusOK || reply["_type"] != "noop" {
		t.Fatalf("adopted device: %d %v", status, reply)
	}

	// Forgetting it does not remove it yet: it has to be reset first.
	res, err := devices.ForgetDevice(ctx, &configv1.ForgetDeviceRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetDevice().GetForgetting() || res.GetDevice().GetState() != configv1.DeviceState_DEVICE_STATE_ADOPTED {
		t.Fatalf("forget returned %v, want the device marked as forgetting", res.GetDevice())
	}
	got, err := devices.GetDevice(ctx, &configv1.GetDeviceRequest{Id: id})
	if err != nil || !got.GetDevice().GetForgetting() {
		t.Fatalf("get = %v, %v", got, err)
	}
	// Asking again changes nothing; adopting a device that is being reset is refused.
	if again, err := devices.ForgetDevice(ctx, &configv1.ForgetDeviceRequest{Id: id}); err != nil || !again.GetDevice().GetForgetting() {
		t.Fatalf("second forget = %v, %v", again, err)
	}
	_, err = devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: id}})
	wantCode(t, err, connect.CodeFailedPrecondition)

	// The device's next inform brings the reset command; it keeps coming until the device has reset.
	for range 2 {
		if status, reply := d.inform(key, version); status != http.StatusOK || !isSetDefault(reply) {
			t.Fatalf("reply to a forgotten device: %d %v, want a setdefault", status, reply)
		}
	}

	// Reset: it speaks the default key and is dropped, then shows up as new.
	if status, _ := d.inform(inform.DefaultKey(), ""); status != http.StatusNotFound {
		t.Fatalf("reset device: status = %d, want 404", status)
	}
	_, err = devices.GetDevice(ctx, &configv1.GetDeviceRequest{Id: id})
	wantCode(t, err, connect.CodeNotFound)
	d.inform(inform.DefaultKey(), "")
	pending, err = devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	if err != nil || len(pending.GetDevices()) != 1 || pending.GetDevices()[0].GetLocked() || pending.GetDevices()[0].GetForgetting() {
		t.Fatalf("pending after the reset = %v, %v", pending, err)
	}
	if _, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: pending.GetDevices()[0].GetId()}}); err != nil {
		t.Fatalf("adopting the reset device again: %v", err)
	}
}

// force gives up waiting for the reset (also as DELETE ...?force=true over
// REST) and removes the device at once; it is then locked until reset by hand.
func TestForceForgetDevice(t *testing.T) {
	f, _ := newUnifiFixture(t)
	ctx := t.Context()
	devices := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))
	d := unifiDevice{t: t, f: f, mac: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x30, 0x02}}

	d.inform(inform.DefaultKey(), "")
	pending, _ := devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	id := pending.GetDevices()[0].GetId()
	if _, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: id}}); err != nil {
		t.Fatal(err)
	}
	_, reply := d.inform(inform.DefaultKey(), "")
	key, version := authKeyOf(t, reply)
	d.inform(key, version)

	if res, err := devices.ForgetDevice(ctx, &configv1.ForgetDeviceRequest{Id: id}); err != nil || !res.GetDevice().GetForgetting() {
		t.Fatalf("forget = %v, %v", res, err)
	}
	code, body, _ := f.rest(t, http.MethodDelete, "/api/v1/devices/"+id+"?force=true", f.adminToken, "")
	if code != http.StatusOK || strings.Contains(string(body), "forgetting") {
		t.Fatalf("forced forget = %d %s, want 200 and the device gone", code, body)
	}
	_, err := devices.GetDevice(ctx, &configv1.GetDeviceRequest{Id: id})
	wantCode(t, err, connect.CodeNotFound)
	// The device still holds the key beenfar dropped: nothing opens it, so it is locked.
	if status, _ := d.inform(key, version); status != http.StatusNotFound {
		t.Fatalf("old key: status = %d, want 404", status)
	}
	pending, err = devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	if err != nil || len(pending.GetDevices()) != 1 || !pending.GetDevices()[0].GetLocked() {
		t.Fatalf("pending = %v, %v, want one locked device", pending, err)
	}
}

// isSetDefault reports whether reply is the factory-reset command.
func isSetDefault(reply map[string]any) bool {
	return reply["_type"] == "cmd" && reply["cmd"] == "setdefault"
}
