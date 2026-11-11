package unifi_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jacobalberty/go-unifi/inform"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jacobalberty/beenfar/adapters/unifi"
	"github.com/jacobalberty/beenfar/api/adapter"
	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
)

type env struct {
	t       *testing.T
	st      *sqlite.Store
	ad      *unifi.Adapter
	devices *core.DeviceService
	srv     *httptest.Server
	spans   *tracetest.SpanRecorder
	metrics *sdkmetric.ManualReader
}

func newEnv(t *testing.T, opts unifi.Options) *env {
	t.Helper()
	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "beenfar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	spans := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	opts.Meter = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
	opts.Tracer = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)).Tracer("test")
	ad, err := unifi.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := adapterhost.NewRegistry(adapterhost.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.AddBuiltin(ad); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reg.Start(ctx)
	t.Cleanup(func() { cancel(); reg.Wait() })

	mux := http.NewServeMux()
	for pattern, h := range reg.HTTPHandlers() {
		mux.Handle(pattern, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Start runs in a goroutine; informs are refused until it has.
	waitReady(t, ad)
	return &env{t: t, st: st, ad: ad, devices: core.New(st, reg).Devices, srv: srv, spans: spans, metrics: reader}
}

// waitReady waits for the adapter's Start to have run: until then it refuses
// to do anything, and Forget on an unknown device is its cheapest probe.
func waitReady(t *testing.T, ad *unifi.Adapter) {
	t.Helper()
	for range 500 {
		if err := ad.Forget(t.Context(), adapter.Device{ID: "probe"}); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("adapter did not start")
}

type device struct {
	mac   net.HardwareAddr
	flags inform.Flags
	model string
}

func newDevice(last byte) *device {
	return &device{
		mac:   net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, last},
		flags: inform.FlagEncrypted | inform.FlagSnappy,
		model: "U7PG2",
	}
}

// post sends an inform sealed with key and returns the status and, for a
// 200, the decoded reply.
func (e *env) post(d *device, key []byte, cfgversion string) (int, map[string]any) {
	e.t.Helper()
	msg, _ := json.Marshal(map[string]any{
		"mac": d.mac.String(), "model": d.model, "version": "4.0.66.10832", "cfgversion": cfgversion,
		"default": len(cfgversion) == 0,
	})
	return e.postRaw(d, key, msg)
}

func (e *env) postRaw(d *device, key, msg []byte) (int, map[string]any) {
	e.t.Helper()
	p, err := inform.Seal(d.mac, d.flags, key, msg)
	if err != nil {
		e.t.Fatal(err)
	}
	wire, err := p.MarshalBinary()
	if err != nil {
		e.t.Fatal(err)
	}
	return e.send(d, key, wire)
}

func (e *env) send(d *device, key, wire []byte) (int, map[string]any) {
	e.t.Helper()
	resp, err := http.Post(e.srv.URL+"/inform", "application/x-binary", bytes.NewReader(wire))
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	p, err := inform.Parse(body)
	if err != nil {
		e.t.Fatalf("parsing reply: %v", err)
	}
	if p.Flags != d.flags && d.flags != 0 {
		e.t.Errorf("reply flags %v, want the device's %v", p.Flags, d.flags)
	}
	plain, err := p.Decode(key)
	if err != nil {
		e.t.Fatalf("reply is not sealed with the key the device used: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(plain, &out); err != nil {
		e.t.Fatal(err)
	}
	return resp.StatusCode, out
}

func (e *env) deviceByMAC(d *device) store.Device {
	e.t.Helper()
	got, err := e.st.GetDeviceByMAC(e.t.Context(), d.mac.String())
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

func (e *env) secret(id, name string) []byte {
	e.t.Helper()
	b, err := e.st.GetDeviceSecret(e.t.Context(), id, name)
	if err != nil {
		return nil
	}
	return b
}

// mgmt parses the mgmt_cfg of a setparam reply.
func mgmt(t *testing.T, reply map[string]any) *inform.Config {
	t.Helper()
	if reply["_type"] != "setparam" {
		t.Fatalf("reply = %v, want a setparam", reply)
	}
	c, err := inform.ParseConfig(reply["mgmt_cfg"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func authKey(t *testing.T, reply map[string]any) []byte {
	t.Helper()
	v, _ := mgmt(t, reply).Get(inform.KeyAuthKey)
	k, err := inform.ParseKey(v)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestUnknownDeviceIsReportedAndRefused(t *testing.T) {
	e := newEnv(t, unifi.Options{})
	d := newDevice(1)

	status, _ := e.post(d, inform.DefaultKey(), "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	got := e.deviceByMAC(d)
	if got.State != store.DeviceStatePending || got.Adapter != "unifi" || got.Model != "U7PG2" || got.IP != "127.0.0.1" || got.Locked {
		t.Fatalf("device = %+v", got)
	}
	// Informing again keeps it pending and refreshes it.
	if status, _ := e.post(d, inform.DefaultKey(), ""); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

func TestAdoptionAndKeyLifecycle(t *testing.T) {
	ctx := t.Context()
	for name, flags := range map[string]inform.Flags{
		"cbc": inform.FlagEncrypted | inform.FlagSnappy,
		"gcm": inform.FlagEncrypted | inform.FlagGCM | inform.FlagZlib,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, unifi.Options{Interval: 7})
			d := newDevice(2)
			d.flags = flags

			if status, _ := e.post(d, inform.DefaultKey(), ""); status != http.StatusNotFound {
				t.Fatalf("status = %d", status)
			}
			dev := e.deviceByMAC(d)
			if _, err := e.devices.Adopt(ctx, dev.ID, ""); err != nil {
				t.Fatal(err)
			}

			// The next inform, still under the default key, gets a new key.
			status, reply := e.post(d, inform.DefaultKey(), "")
			if status != http.StatusOK {
				t.Fatalf("status = %d", status)
			}
			newKey := authKey(t, reply)
			if len(newKey) != inform.KeySize || bytes.Equal(newKey, inform.DefaultKey()) {
				t.Fatalf("new key = %x", newKey)
			}
			version, _ := mgmt(t, reply).Get(inform.KeyCfgVersion)
			if len(version) != 16 || reply["cfg_version"] != version {
				t.Fatalf("cfgversion %q, cfg_version %v", version, reply["cfg_version"])
			}
			if _, set := mgmt(t, reply).Get(inform.KeyUseAESGCM); set {
				t.Error("GCM was not asked for")
			}
			if got := e.secret(dev.ID, "pending"); !bytes.Equal(got, newKey) || e.secret(dev.ID, "current") != nil {
				t.Fatalf("pending %x current %x", got, e.secret(dev.ID, "current"))
			}

			// The reply was lost: the device still uses the default key, and
			// is sent the same key again.
			_, again := e.post(d, inform.DefaultKey(), "")
			if !bytes.Equal(authKey(t, again), newKey) {
				t.Fatal("a second reply carried a different key")
			}

			// The device switched. The key is committed and the others go.
			status, reply = e.post(d, newKey, version)
			if status != http.StatusOK || reply["_type"] != "noop" || reply["interval"] != float64(7) {
				t.Fatalf("after the switch: %d %v", status, reply)
			}
			if !bytes.Equal(e.secret(dev.ID, "current"), newKey) || e.secret(dev.ID, "pending") != nil {
				t.Fatal("key was not committed")
			}

			// The old (default) key is rejected now, and the device is locked.
			if status, _ := e.post(d, inform.DefaultKey(), ""); status != http.StatusNotFound {
				t.Fatalf("old key: status = %d, want 404", status)
			}
			if !e.deviceByMAC(d).Locked {
				t.Error("device using a rejected key is not marked locked")
			}
			// ... until it informs under its key again.
			if status, _ := e.post(d, newKey, version); status != http.StatusOK {
				t.Fatalf("status = %d", status)
			}
			if got := e.deviceByMAC(d); got.Locked || got.State != store.DeviceStateAdopted || got.Firmware != "4.0.66.10832" {
				t.Fatalf("device = %+v", got)
			}
		})
	}
}

func TestConfigRevisionPushesSetParam(t *testing.T) {
	ctx := t.Context()
	e := newEnv(t, unifi.Options{})
	d := newDevice(3)
	e.post(d, inform.DefaultKey(), "")
	dev := e.deviceByMAC(d)
	if _, err := e.devices.Adopt(ctx, dev.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, reply := e.post(d, inform.DefaultKey(), "")
	key := authKey(t, reply)
	version, _ := mgmt(t, reply).Get(inform.KeyCfgVersion)
	e.post(d, key, version)

	// A device on an old config is sent the current one, with its own key.
	status, reply := e.post(d, key, "0000000000000001")
	if status != http.StatusOK || !bytes.Equal(authKey(t, reply), key) {
		t.Fatalf("stale device: %d %v", status, reply)
	}
	if v, _ := mgmt(t, reply).Get(inform.KeyCfgVersion); v != version {
		t.Fatalf("cfgversion %q, want %q", v, version)
	}
	// system_cfg stays empty until its key names are verified.
	if _, ok := reply["system_cfg"]; ok {
		t.Errorf("unexpected system_cfg: %v", reply["system_cfg"])
	}

	// Changing the configuration changes the revision it is sent.
	if _, err := e.st.CreateWifiNetwork(ctx, store.WifiNetwork{SSID: "home", Security: store.WifiSecurityOpen, Band: store.WifiBandBoth}); err != nil {
		t.Fatal(err)
	}
	_, reply = e.post(d, key, version)
	if v, _ := mgmt(t, reply).Get(inform.KeyCfgVersion); v == version || len(v) != 16 {
		t.Fatalf("cfgversion after a config change = %q (was %q)", v, version)
	}
}

func TestKeyRotation(t *testing.T) {
	ctx := t.Context()
	e := newEnv(t, unifi.Options{})
	d := newDevice(4)
	e.post(d, inform.DefaultKey(), "")
	dev := e.deviceByMAC(d)
	if _, err := e.devices.Adopt(ctx, dev.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, reply := e.post(d, inform.DefaultKey(), "")
	oldKey := authKey(t, reply)
	version, _ := mgmt(t, reply).Get(inform.KeyCfgVersion)
	e.post(d, oldKey, version)

	if err := e.ad.Rekey(ctx, adapterDevice(dev)); err != nil {
		t.Fatal(err)
	}
	status, reply := e.post(d, oldKey, version)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	newKey := authKey(t, reply)
	if bytes.Equal(newKey, oldKey) {
		t.Fatal("rotation kept the key")
	}
	// Until the device switches, the old key keeps working.
	if status, reply := e.post(d, oldKey, version); status != http.StatusOK || !bytes.Equal(authKey(t, reply), newKey) {
		t.Fatalf("old key before the switch: %d %v", status, reply)
	}
	if status, reply := e.post(d, newKey, version); status != http.StatusOK || reply["_type"] != "noop" {
		t.Fatalf("new key: %d %v", status, reply)
	}
	if status, _ := e.post(d, oldKey, version); status != http.StatusNotFound {
		t.Fatalf("old key after the switch: status = %d, want 404", status)
	}
}

func TestUndecryptableDeviceIsLocked(t *testing.T) {
	ctx := t.Context()
	e := newEnv(t, unifi.Options{})
	d := newDevice(5)

	foreign := inform.NewKey()
	if status, _ := e.post(d, foreign, "abcdef0123456789"); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	got := e.deviceByMAC(d)
	if !got.Locked || got.State != store.DeviceStatePending || got.IP != "127.0.0.1" {
		t.Fatalf("device = %+v", got)
	}
	locked, err := e.st.ListDevices(ctx, store.DeviceFilter{State: store.DeviceStatePending})
	if err != nil || len(locked) != 1 || !locked[0].Locked {
		t.Fatalf("list = %+v, %v", locked, err)
	}
	// A locked device cannot be adopted: nothing could be sent to it.
	if _, err := e.devices.Adopt(ctx, got.ID, ""); err == nil {
		t.Fatal("adopted a locked device")
	}
}

// Real captures decrypt once their device's key is known, and the device is
// then reported with its real model.
func TestRealCaptures(t *testing.T) {
	ctx := t.Context()
	for _, c := range []struct {
		file, key, mac, flags string
	}{
		{"aes_snappy.dat", "e2c930683af3945e4d0d58d37a78c2a6", "f0:9f:c2:79:63:90", "encrypted|snappy"},
		{"gcm_zlib.dat", "05223125e6b44aca0a0599551168d766", "fc:ec:da:fc:c5:35", "encrypted|zlib|gcm"},
	} {
		t.Run(c.file, func(t *testing.T) {
			e := newEnv(t, unifi.Options{})
			wire, err := os.ReadFile(filepath.Join("testdata", c.file))
			if err != nil {
				t.Fatal(err)
			}
			p, err := inform.Parse(wire)
			if err != nil || p.MAC.String() != c.mac || p.Flags.String() != c.flags {
				t.Fatalf("capture = %+v, %v", p, err)
			}
			d := &device{mac: p.MAC, flags: p.Flags}

			// Nobody holds the key: the device is locked.
			if status, _ := e.send(d, nil, wire); status != http.StatusNotFound {
				t.Fatalf("status = %d", status)
			}
			dev := e.deviceByMAC(d)
			if !dev.Locked || dev.Model != "" {
				t.Fatalf("device = %+v", dev)
			}

			// With its key imported, the same packet decrypts.
			key, _ := hex.DecodeString(c.key)
			if err := e.st.SetDeviceSecret(ctx, dev.ID, unifi.SecretImported, key); err != nil {
				t.Fatal(err)
			}
			if status, _ := e.send(d, key, wire); status != http.StatusNotFound {
				t.Fatalf("status = %d (pending devices are answered 404)", status)
			}
			got := e.deviceByMAC(d)
			if got.Locked || got.Model != "U7PG2" || got.Firmware == "" {
				t.Fatalf("device = %+v", got)
			}
		})
	}
}

func TestInformSpan(t *testing.T) {
	e := newEnv(t, unifi.Options{})
	d := newDevice(6)
	e.post(d, inform.DefaultKey(), "")
	dev := e.deviceByMAC(d)
	if _, err := e.devices.Adopt(t.Context(), dev.ID, ""); err != nil {
		t.Fatal(err)
	}
	e.spans.Reset()
	e.post(d, inform.DefaultKey(), "")

	var found int
	for _, s := range e.spans.Ended() {
		if s.Name() != "unifi.inform" {
			continue
		}
		found++
		attrs := map[string]string{}
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = kv.Value.String()
		}
		if attrs["device.mac"] != d.mac.String() || attrs["unifi.flags"] != "encrypted|snappy" || attrs["unifi.response"] != "setparam" {
			t.Errorf("attributes = %v", attrs)
		}
	}
	if found != 1 {
		t.Fatalf("%d unifi.inform spans, want 1", found)
	}
}

func adapterDevice(d store.Device) adapter.Device {
	return adapter.Device{ID: d.ID, MAC: d.MAC, State: adapter.DeviceState(d.State)}
}

// sums returns a counter's values by the value of one attribute ("" for none).
func (e *env) sums(name, attr string) map[string]int64 {
	e.t.Helper()
	var rm metricdata.ResourceMetrics
	if err := e.metrics.Collect(e.t.Context(), &rm); err != nil {
		e.t.Fatal(err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				v, _ := dp.Attributes.Value(attribute.Key(attr))
				out[v.String()] += dp.Value
			}
		}
	}
	return out
}

func TestMetrics(t *testing.T) {
	ctx := t.Context()
	e := newEnv(t, unifi.Options{})
	d := newDevice(7)

	e.post(d, inform.DefaultKey(), "")           // unknown: pending
	e.post(newDevice(8), inform.NewKey(), "abc") // no key opens it
	dev := e.deviceByMAC(d)
	if _, err := e.devices.Adopt(ctx, dev.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, reply := e.post(d, inform.DefaultKey(), "") // key push
	key := authKey(t, reply)
	version, _ := mgmt(t, reply).Get(inform.KeyCfgVersion)
	e.post(d, key, version)            // noop
	e.post(d, key, "0000000000000001") // revision push

	if got := e.sums("beenfar.unifi.informs.received", "")[""]; got != 5 {
		t.Errorf("received = %d, want 5", got)
	}
	rej := e.sums("beenfar.unifi.informs.rejected", "reason")
	if rej["not_adopted"] != 1 || rej["undecryptable"] != 1 || len(rej) != 2 {
		t.Errorf("rejected = %v", rej)
	}
	push := e.sums("beenfar.unifi.config_pushes", "reason")
	if push["key"] != 1 || push["revision"] != 1 || len(push) != 2 {
		t.Errorf("pushes = %v", push)
	}
}

// adoptFully takes a device from unknown to running on its own key and
// returns that key and the cfgversion it was sent.
func (e *env) adoptFully(d *device) (store.Device, []byte, string) {
	e.t.Helper()
	e.post(d, inform.DefaultKey(), "")
	dev := e.deviceByMAC(d)
	if _, err := e.devices.Adopt(e.t.Context(), dev.ID, ""); err != nil {
		e.t.Fatal(err)
	}
	_, reply := e.post(d, inform.DefaultKey(), "")
	key := authKey(e.t, reply)
	version, _ := mgmt(e.t, reply).Get(inform.KeyCfgVersion)
	if status, reply := e.post(d, key, version); status != http.StatusOK || reply["_type"] != "noop" {
		e.t.Fatalf("after the switch: %d %v", status, reply)
	}
	return dev, key, version
}

// A device that was adopted and comes back on the default key was reset. It
// stays the same adopted device and is marked as awaiting re-adoption, but is
// sent nothing until somebody accepts: anybody can send an inform sealed with
// the default key, and the reply would be readable by them too.
func TestReadoptionNeedsAcceptance(t *testing.T) {
	ctx := t.Context()
	const window = 400 * time.Millisecond
	e := newEnv(t, unifi.Options{ReadoptAfter: window})
	d := newDevice(9)
	dev, key1, version := e.adoptFully(d)

	// While the device is being heard from, the default key opens nothing: a
	// spoofed inform neither flags the device nor gets a key.
	for range 3 {
		if status, _ := e.post(d, inform.DefaultKey(), ""); status != http.StatusNotFound {
			t.Fatalf("default key on a live device: status = %d, want 404", status)
		}
	}
	if got := e.deviceByMAC(d); got.Readoption != store.ReadoptionNone || !got.Locked || e.secret(dev.ID, "pending") != nil {
		t.Fatalf("device = %+v, pending key %x", got, e.secret(dev.ID, "pending"))
	}

	// Those informs did not count as hearing from the device, so its silence
	// runs from its last real inform. Once it has been quiet long enough, the
	// default key identifies it as reset, and nothing is sent.
	time.Sleep(window + 100*time.Millisecond)
	for range 2 {
		if status, _ := e.post(d, inform.DefaultKey(), ""); status != http.StatusNotFound {
			t.Fatalf("reset device: status = %d, want 404 until accepted", status)
		}
	}
	got := e.deviceByMAC(d)
	if got.ID != dev.ID || got.State != store.DeviceStateAdopted || got.Readoption != store.ReadoptionAwaiting || got.Locked {
		t.Fatalf("device = %+v, want the same adopted device, awaiting re-adoption", got)
	}
	if e.secret(dev.ID, "pending") != nil {
		t.Fatal("a key was created before the re-adoption was accepted")
	}

	// Accepting is the call that adopts a new device. Until the device uses
	// its new key it is being re-adopted.
	accepted, err := e.devices.Adopt(ctx, dev.ID, "")
	if err != nil || accepted.Readoption != store.ReadoptionInProgress || accepted.State != store.DeviceStateAdopted {
		t.Fatalf("accept = %+v, %v", accepted, err)
	}
	status, reply := e.post(d, inform.DefaultKey(), "")
	if status != http.StatusOK {
		t.Fatalf("accepted device: status = %d", status)
	}
	key2 := authKey(t, reply)
	if bytes.Equal(key2, key1) || bytes.Equal(key2, inform.DefaultKey()) {
		t.Fatal("readoption did not produce a new key")
	}
	// The reply is lost: the same key is sent again.
	_, again := e.post(d, inform.DefaultKey(), "")
	if !bytes.Equal(authKey(t, again), key2) || e.deviceByMAC(d).Readoption != store.ReadoptionInProgress {
		t.Fatal("a repeated reply changed the key or the state")
	}

	// It was not reset after all: it comes back on its own key. The key meant
	// for a reset device (sealed with the public default key) is dropped.
	if status, reply := e.post(d, key1, version); status != http.StatusOK || reply["_type"] != "noop" {
		t.Fatalf("on its own key: %d %v", status, reply)
	}
	if e.secret(dev.ID, "pending") != nil || e.deviceByMAC(d).Readoption != store.ReadoptionNone {
		t.Fatal("the readoption key survived the device coming back")
	}
	if status, _ := e.post(d, inform.DefaultKey(), ""); status != http.StatusNotFound {
		t.Fatalf("default key right after contact: status = %d, want 404", status)
	}

	// A real reset again: silent, awaiting, accepted, re-keyed.
	time.Sleep(window + 100*time.Millisecond)
	if status, _ := e.post(d, inform.DefaultKey(), ""); status != http.StatusNotFound {
		t.Fatalf("status = %d", status)
	}
	if _, err := e.devices.Adopt(ctx, dev.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, reply = e.post(d, inform.DefaultKey(), "")
	key3 := authKey(t, reply)
	if bytes.Equal(key3, key2) {
		t.Fatal("the dropped key was reused")
	}
	if status, reply := e.post(d, key3, version); status != http.StatusOK || reply["_type"] != "noop" {
		t.Fatalf("on the new key: %d %v", status, reply)
	}
	got = e.deviceByMAC(d)
	if got.Readoption != store.ReadoptionNone || got.State != store.DeviceStateAdopted || got.Locked {
		t.Fatalf("device = %+v, want adopted and no longer being re-adopted", got)
	}
	if !bytes.Equal(e.secret(dev.ID, "current"), key3) || e.secret(dev.ID, "pending") != nil {
		t.Fatal("the new key was not committed")
	}
	if status, _ := e.post(d, key1, version); status != http.StatusNotFound {
		t.Fatalf("the key from before the reset: status = %d, want 404", status)
	}

	if push := e.sums("beenfar.unifi.config_pushes", "reason"); push["readopt"] != 3 {
		t.Errorf("pushes = %v, want 3 readopt (a reply and its repeat, then another reply)", push)
	}
	if rej := e.sums("beenfar.unifi.informs.rejected", "reason"); rej["awaiting_readoption"] != 3 {
		t.Errorf("rejected = %v, want 3 awaiting_readoption", rej)
	}
}

// With ReadoptAfter at zero a reset device stays locked, however long it is
// silent, and can only be dealt with by forgetting it.
func TestReadoptionCanBeTurnedOff(t *testing.T) {
	e := newEnv(t, unifi.Options{})
	d := newDevice(10)
	dev, _, _ := e.adoptFully(d)
	time.Sleep(50 * time.Millisecond)
	if status, _ := e.post(d, inform.DefaultKey(), ""); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if got := e.deviceByMAC(d); !got.Locked || got.Readoption != store.ReadoptionNone || e.secret(dev.ID, "pending") != nil {
		t.Fatalf("device = %+v", got)
	}
}
