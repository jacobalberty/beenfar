//go:build e2e

// Package e2e runs the real beenfard binary against a fleet of emulated UniFi
// devices (github.com/jamesbraid/unifi-emu) and drives it through the public
// API, the way an operator or the Terraform provider would. Nothing here is
// in-process: the daemon is a subprocess with its own database, the devices
// speak the real inform protocol to its /inform endpoint, and every assertion
// is made on API responses or on what the devices themselves observe.
//
// The suite is behind the e2e build tag so a plain "go test ./..." (and the
// Dockerfile's build stage) stays free of subprocesses and slow polling:
//
//	go test -tags e2e -count=1 ./e2e/...
//
// BEENFAR_E2E_BIN points the suite at a prebuilt beenfard instead of building
// one from the checkout, e.g. one copied out of the release image.
package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jacobalberty/go-unifi/inform"
	emu "github.com/jamesbraid/unifi-emu"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
)

// beenfardBin is the daemon under test, set by TestMain.
var beenfardBin string

func TestMain(m *testing.M) { os.Exit(run(m)) }

func run(m *testing.M) int {
	if bin := os.Getenv("BEENFAR_E2E_BIN"); bin != "" {
		abs, err := filepath.Abs(bin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "BEENFAR_E2E_BIN:", err)
			return 1
		}
		beenfardBin = abs
		return m.Run()
	}
	dir, err := os.MkdirTemp("", "beenfar-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	beenfardBin = filepath.Join(dir, "beenfard")
	build := exec.Command("go", "build", "-o", beenfardBin, "../cmd/beenfard")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building beenfard:", err)
		return 1
	}
	return m.Run()
}

// daemon is a running beenfard.
type daemon struct {
	url string // base URL, no trailing slash
	// client talks to the API as the admin, with an API token. Build a client
	// for any service from it, e.g. configv1connect.NewWifiNetworkServiceClient(d.client).
	client  *connect.Client
	devices configv1connect.DeviceServiceClient
}

// startDaemon runs beenfard on a free loopback port with a fresh database and
// the fastest inform interval, and returns once it serves /healthz. env adds
// to (and overrides) the baseline settings. The process is stopped, and its
// log shown if the test failed, when the test ends.
func startDaemon(t *testing.T, env ...string) *daemon {
	t.Helper()
	port := freePort(t)
	dir := t.TempDir()
	db := filepath.Join(dir, "beenfar.db")
	environ := append(os.Environ(),
		"BEENFAR_LISTEN=127.0.0.1:"+port,
		"BEENFAR_DB=sqlite://"+db,
		"BEENFAR_LOG_FORMAT=text",
		"BEENFAR_ADMIN_PASSWORD=e2e-admin-password",
		"BEENFAR_UNIFI_INFORM_INTERVAL=1",
	)
	environ = append(environ, env...) // later entries win

	logs := &syncBuffer{}
	cmd := exec.Command(beenfardBin)
	cmd.Env = environ
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting beenfard: %v", err)
	}
	exited := make(chan struct{}) // closed once the process is gone, with waitErr set
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		if t.Failed() {
			t.Logf("beenfard output:\n%s", logs.String())
		}
	})

	url := "http://127.0.0.1:" + port
	eventually(t, 30*time.Second, "beenfard to serve /healthz", func(ctx context.Context) error {
		select {
		case <-exited:
			return fmt.Errorf("beenfard exited early: %v\n%s", waitErr, logs.String())
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("/healthz = %s", resp.Status)
		}
		return nil
	})

	// The admin exists once the daemon is up (created from
	// BEENFAR_ADMIN_PASSWORD), so an API token can be issued offline against
	// the same database, as the README tells operators to.
	tokenCmd := exec.Command(beenfardBin, "admin", "token", "-db", "sqlite://"+db, "-name", "e2e")
	tokenCmd.Env = environ
	var stderr bytes.Buffer
	tokenCmd.Stderr = &stderr
	out, err := tokenCmd.Output()
	if err != nil {
		t.Fatalf("beenfard admin token: %v\n%s", err, stderr.String())
	}
	token := strings.TrimSpace(string(out))

	auth := func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, spec)
		}
	}
	client := connect.NewClient(connecthttp.NewTransport(http.DefaultClient, url), auth)
	return &daemon{url: url, client: client, devices: configv1connect.NewDeviceServiceClient(client)}
}

// fleet is a set of emulated devices informing a daemon.
type fleet struct {
	*emu.Emu
	specs []emu.DeviceSpec
	// tap has seen every inform the devices sent.
	tap *informTap
}

// informTap records the encoding flags of every inform packet that passes
// through it, by device MAC. The devices inform the daemon through it, so a
// test can check how they actually talk to the daemon (for instance that
// they switched to AES-GCM) rather than only that they keep being answered.
type informTap struct {
	mu    sync.Mutex
	flags map[string][]inform.Flags // in order of arrival
}

// handler records each inform's flags and forwards the request to target.
func (tp *informTap) handler(target *url.URL) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(target)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if p, err := inform.Parse(body); err == nil {
			tp.mu.Lock()
			tp.flags[p.MAC.String()] = append(tp.flags[p.MAC.String()], p.Flags)
			tp.mu.Unlock()
		}
		proxy.ServeHTTP(w, r)
	})
}

// count returns how many informs from mac (all devices when mac is empty)
// had every flag in f set.
func (tp *informTap) count(mac string, f inform.Flags) int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	n := 0
	for m, all := range tp.flags {
		if mac != "" && m != mac {
			continue
		}
		for _, got := range all {
			if got&f == f {
				n++
			}
		}
	}
	return n
}

// waitInforms waits until mac has sent at least n informs with the flags f.
func (tp *informTap) waitInforms(t *testing.T, mac string, f inform.Flags, n int) {
	t.Helper()
	eventually(t, 15*time.Second, fmt.Sprintf("%d informs from %s with flags %s", n, mac, f), func(context.Context) error {
		if got := tp.count(mac, f); got < n {
			return fmt.Errorf("%d so far", got)
		}
		return nil
	})
}

// startFleet starts the given devices informing d once a second.
func startFleet(t *testing.T, d *daemon, specs ...emu.DeviceSpec) *fleet {
	t.Helper()
	// The emulator logs through the standard logger; keep that in the test log.
	log.SetOutput(testWriter{t})
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	// The devices inform the daemon through a tap that records their packets.
	target, err := url.Parse(d.url)
	if err != nil {
		t.Fatal(err)
	}
	tap := &informTap{flags: map[string][]inform.Flags{}}
	proxy := httptest.NewServer(tap.handler(target))
	t.Cleanup(proxy.Close)

	e := emu.New(proxy.URL+"/inform", emu.WithInformInterval(time.Second))
	if err := e.Add(specs...); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	return &fleet{Emu: e, specs: specs, tap: tap}
}

// The devices the tests use. MACs are fixed so failures are reproducible; the
// models are three different device types, so both the access point and the
// non-access-point paths run.
var (
	accessPoint = emu.DeviceSpec{MAC: "00:27:22:e0:00:01", Model: "U7PRO", Name: "e2e-ap"}
	switchDev   = emu.DeviceSpec{MAC: "00:27:22:e0:00:02", Model: "USM8P", Name: "e2e-switch"}
	gateway     = emu.DeviceSpec{MAC: "00:27:22:e0:00:03", Model: "UGW3", Name: "e2e-gateway"}
	// A model beenfar has a profile for, so it is sent a complete system.cfg.
	profiledAP = emu.DeviceSpec{MAC: "00:27:22:e0:00:04", Model: "U7LT", Name: "e2e-ap-lite"}
)

// pending waits until the daemon lists exactly the devices with the given
// MACs as pending, and returns them by MAC.
func (d *daemon) pending(t *testing.T, macs ...string) map[string]*configv1.Device {
	t.Helper()
	var got map[string]*configv1.Device
	eventually(t, 30*time.Second, fmt.Sprintf("%d pending devices", len(macs)), func(ctx context.Context) error {
		resp, err := d.devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
		if err != nil {
			return err
		}
		got = map[string]*configv1.Device{}
		for _, dev := range resp.GetDevices() {
			got[dev.GetMac()] = dev
		}
		for _, mac := range macs {
			if got[mac] == nil {
				return fmt.Errorf("%s is not pending yet (pending: %d)", mac, len(got))
			}
		}
		return nil
	})
	return got
}

func (d *daemon) adopt(t *testing.T, dev *configv1.Device) {
	t.Helper()
	_, err := d.devices.AdoptDevice(callCtx(t), &configv1.AdoptDeviceRequest{
		Device: &configv1.AdoptDeviceRequest_Id{Id: dev.GetId()},
	})
	if err != nil {
		t.Fatalf("adopting %s: %v", dev.GetMac(), err)
	}
}

// state returns the daemon's view of the device with the given MAC.
func (d *daemon) state(ctx context.Context, mac string) (configv1.DeviceState, error) {
	resp, err := d.devices.ListDevices(ctx, &configv1.ListDevicesRequest{})
	if err != nil {
		return 0, err
	}
	for _, dev := range resp.GetDevices() {
		if dev.GetMac() == mac {
			return dev.GetState(), nil
		}
	}
	return 0, fmt.Errorf("%s is not listed", mac)
}

// connected waits until the emulated device has finished the adoption
// handshake (it informed under its new key and got an answer) and the daemon
// reports it adopted.
func connected(t *testing.T, d *daemon, f *fleet, mac string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := f.WaitState(ctx, mac, emu.StateConnected); err != nil {
		t.Fatalf("device side: %v", err)
	}
	eventually(t, 15*time.Second, mac+" to be adopted in the daemon", func(ctx context.Context) error {
		st, err := d.state(ctx, mac)
		if err != nil {
			return err
		}
		if st != configv1.DeviceState_DEVICE_STATE_ADOPTED {
			return fmt.Errorf("state = %s", st)
		}
		return nil
	})
}

// A mixed fleet shows up as pending with the identity each device reports,
// stays pending (nothing is adopted by itself), and is adopted through the
// API: every device then completes the real key handshake and keeps informing.
func TestFleetAdoption(t *testing.T) {
	d := startDaemon(t)
	f := startFleet(t, d, accessPoint, switchDev, gateway)

	pending := d.pending(t, accessPoint.MAC, switchDev.MAC, gateway.MAC)
	for _, spec := range f.specs {
		dev := pending[spec.MAC]
		if dev.GetAdapter() != "unifi" {
			t.Errorf("%s adapter = %q, want unifi", spec.MAC, dev.GetAdapter())
		}
		if dev.GetModel() != spec.Model {
			t.Errorf("%s model = %q, want %q", spec.MAC, dev.GetModel(), spec.Model)
		}
		if dev.GetState() != configv1.DeviceState_DEVICE_STATE_PENDING {
			t.Errorf("%s state = %s, want pending", spec.MAC, dev.GetState())
		}
		if st, _ := f.State(spec.MAC); st != emu.StatePending {
			t.Errorf("%s device-side state = %s, want PENDING", spec.MAC, st)
		}
	}

	for _, spec := range f.specs {
		d.adopt(t, pending[spec.MAC])
	}
	for _, spec := range f.specs {
		connected(t, d, f, spec.MAC)
	}

	// Adopted devices go on informing: last_seen_time keeps moving.
	waitLastSeenAdvances(t, d, accessPoint.MAC)

	// AES-GCM is off by default: no device was told to switch to it.
	if n := f.tap.count("", inform.FlagGCM); n != 0 {
		t.Errorf("%d informs used AES-GCM without BEENFAR_UNIFI_GCM", n)
	}
	// Adopted devices do use their own key, though: encrypted with AES-CBC.
	for _, spec := range f.specs {
		if f.tap.count(spec.MAC, inform.FlagEncrypted) == 0 {
			t.Errorf("%s never sent an encrypted inform", spec.MAC)
		}
	}
}

// With AES-GCM switched on, devices adopted afterwards are told to move to it
// and carry on talking to the daemon through the switch. Informs are
// observed on the wire: before adoption they are AES-CBC, afterwards they
// carry the GCM flag, and the daemon goes on accepting them.
func TestAdoptionWithAESGCM(t *testing.T) {
	d := startDaemon(t, "BEENFAR_UNIFI_GCM=true")
	f := startFleet(t, d, accessPoint, switchDev)

	pending := d.pending(t, accessPoint.MAC, switchDev.MAC)
	for _, spec := range f.specs {
		// A pending device is on the default key, and uses CBC with it.
		f.tap.waitInforms(t, spec.MAC, inform.FlagEncrypted, 1)
		if n := f.tap.count(spec.MAC, inform.FlagGCM); n != 0 {
			t.Fatalf("%s sent %d informs with GCM before it was adopted", spec.MAC, n)
		}
	}
	for _, spec := range f.specs {
		d.adopt(t, pending[spec.MAC])
	}
	for _, spec := range f.specs {
		connected(t, d, f, spec.MAC)
	}
	for _, spec := range f.specs {
		// More than one, so the switch holds rather than happening once.
		f.tap.waitInforms(t, spec.MAC, inform.FlagEncrypted|inform.FlagGCM, 2)
		// Informs after the switch must still be accepted by the daemon.
		waitLastSeenAdvances(t, d, spec.MAC)
	}
}

// Forgetting an adopted device factory resets it: the device receives the
// reset on its next inform, falls back to pending, and the daemon lists it
// as a new pending device that can be adopted again.
func TestForgetResetsAndReadopts(t *testing.T) {
	d := startDaemon(t)
	f := startFleet(t, d, switchDev)

	pending := d.pending(t, switchDev.MAC)
	d.adopt(t, pending[switchDev.MAC])
	connected(t, d, f, switchDev.MAC)

	adopted, err := d.devices.ListDevices(callCtx(t), &configv1.ListDevicesRequest{State: configv1.DeviceState_DEVICE_STATE_ADOPTED})
	if err != nil || len(adopted.GetDevices()) != 1 {
		t.Fatalf("adopted devices = %v, %v", adopted, err)
	}
	if _, err := d.devices.ForgetDevice(callCtx(t), &configv1.ForgetDeviceRequest{Id: adopted.GetDevices()[0].GetId()}); err != nil {
		t.Fatal(err)
	}

	// The device was told to reset itself...
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := f.WaitState(ctx, switchDev.MAC, emu.StatePending); err != nil {
		t.Fatalf("device did not reset: %v", err)
	}
	// ...and the daemon sees it come back as a new, pending device, with
	// nothing left of the forgotten one.
	again := d.pending(t, switchDev.MAC)[switchDev.MAC]
	if again.GetForgetting() {
		t.Error("device is still marked as forgetting")
	}

	// It can be adopted all over again.
	d.adopt(t, again)
	connected(t, d, f, switchDev.MAC)
}

// device returns the daemon's record of the device with the given MAC.
func (d *daemon) device(ctx context.Context, mac string) (*configv1.Device, error) {
	resp, err := d.devices.ListDevices(ctx, &configv1.ListDevicesRequest{})
	if err != nil {
		return nil, err
	}
	for _, dev := range resp.GetDevices() {
		if dev.GetMac() == mac {
			return dev, nil
		}
	}
	return nil, fmt.Errorf("%s is not listed", mac)
}

// The daemon works out what each device is from its inform: the access point
// and the switch are told apart and the switch reports its ports, which is
// what a client uses to decide whether port settings apply.
func TestDevicesReportKindAndPorts(t *testing.T) {
	d := startDaemon(t)
	startFleet(t, d, accessPoint, switchDev, gateway)
	d.pending(t, accessPoint.MAC, switchDev.MAC, gateway.MAC)

	for _, want := range []struct {
		mac       string
		kind      configv1.DeviceKind
		wantPorts bool
	}{
		{accessPoint.MAC, configv1.DeviceKind_DEVICE_KIND_ACCESS_POINT, false},
		{switchDev.MAC, configv1.DeviceKind_DEVICE_KIND_SWITCH, true},
		{gateway.MAC, configv1.DeviceKind_DEVICE_KIND_GATEWAY, false},
	} {
		dev, err := d.device(callCtx(t), want.mac)
		if err != nil {
			t.Fatal(err)
		}
		if dev.GetKind() != want.kind {
			t.Errorf("%s kind = %s, want %s", want.mac, dev.GetKind(), want.kind)
		}
		if (dev.GetPortCount() > 0) != want.wantPorts {
			t.Errorf("%s port_count = %d, want ports: %v", want.mac, dev.GetPortCount(), want.wantPorts)
		}
	}
}

// Port settings only exist for devices with ports: the API refuses them on an
// access point and accepts them on a switch, within its port count.
func TestSwitchPortsOnlyOnDevicesWithPorts(t *testing.T) {
	d := startDaemon(t)
	f := startFleet(t, d, accessPoint, switchDev)
	pending := d.pending(t, accessPoint.MAC, switchDev.MAC)
	d.adopt(t, pending[accessPoint.MAC])
	d.adopt(t, pending[switchDev.MAC])
	connected(t, d, f, accessPoint.MAC)
	connected(t, d, f, switchDev.MAC)

	ports := configv1connect.NewSwitchPortServiceClient(d.client)
	create := func(mac string, index uint32) error {
		dev, err := d.device(callCtx(t), mac)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ports.CreateSwitchPort(callCtx(t), &configv1.CreateSwitchPortRequest{
			SwitchPort: &configv1.SwitchPort{DeviceId: dev.GetId(), PortIndex: index, Name: "e2e"},
		})
		return err
	}

	if err := create(accessPoint.MAC, 1); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("port on an access point: %v, want FailedPrecondition", err)
	}
	if err := create(switchDev.MAC, 1); err != nil {
		t.Errorf("port 1 of the switch: %v", err)
	}
	sw, err := d.device(callCtx(t), switchDev.MAC)
	if err != nil {
		t.Fatal(err)
	}
	if err := create(switchDev.MAC, sw.GetPortCount()+1); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("port beyond the switch's %d ports: %v, want InvalidArgument", sw.GetPortCount(), err)
	}
}

// An access point can be pinned to an AP group and released from it again
// through the API; a switch cannot be put in one.
func TestAccessPointGroupOverride(t *testing.T) {
	d := startDaemon(t)
	f := startFleet(t, d, accessPoint, switchDev)
	pending := d.pending(t, accessPoint.MAC, switchDev.MAC)

	// A wifi network limited to two groups, which exists before the access
	// point is adopted: the access point is provisioned with it as it adopts
	// (its group is unset, so it gets every network) and goes on informing.
	wifi := configv1connect.NewWifiNetworkServiceClient(d.client)
	if _, err := wifi.CreateWifiNetwork(callCtx(t), &configv1.CreateWifiNetworkRequest{WifiNetwork: &configv1.WifiNetwork{
		Ssid: "e2e-upstairs", Security: configv1.WifiSecurity_WIFI_SECURITY_WPA_PERSONAL,
		SecurityKey: "correct horse battery", ApGroups: []string{"upstairs", "attic"},
	}}); err != nil {
		t.Fatal(err)
	}
	d.adopt(t, pending[accessPoint.MAC])
	d.adopt(t, pending[switchDev.MAC])
	connected(t, d, f, accessPoint.MAC)
	connected(t, d, f, switchDev.MAC)
	waitLastSeenAdvances(t, d, accessPoint.MAC)

	ap, err := d.device(callCtx(t), accessPoint.MAC)
	if err != nil {
		t.Fatal(err)
	}
	if ap.GetApGroup() != "" {
		t.Fatalf("new access point is in group %q", ap.GetApGroup())
	}
	set := func(group string) (*configv1.Device, error) {
		resp, err := d.devices.UpdateDevice(callCtx(t), &configv1.UpdateDeviceRequest{
			Device: &configv1.Device{Id: ap.GetId(), ApGroup: group},
		})
		return resp.GetDevice(), err
	}
	if got, err := set("attic"); err != nil || got.GetApGroup() != "attic" {
		t.Fatalf("pinning to attic: %v, %v", got, err)
	}
	if got, err := d.device(callCtx(t), accessPoint.MAC); err != nil || got.GetApGroup() != "attic" {
		t.Fatalf("listed after pinning: %v, %v", got, err)
	}
	// Moving it away and back to the default keeps it adopted and informing.
	if got, err := set(""); err != nil || got.GetApGroup() != "" {
		t.Fatalf("clearing the group: %v, %v", got, err)
	}
	waitLastSeenAdvances(t, d, accessPoint.MAC)

	sw, err := d.device(callCtx(t), switchDev.MAC)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.devices.UpdateDevice(callCtx(t), &configv1.UpdateDeviceRequest{Device: &configv1.Device{Id: sw.GetId(), ApGroup: "attic"}})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("group on a switch: %v, want InvalidArgument", err)
	}
}

// An access point of a model with a profile gets settings it can be changed
// through the API, and is sent a complete system.cfg; one without a profile has
// no settings and is sent none. The emulator does not interpret system_cfg, so
// this only shows that both access points adopt, accept every setparam that
// follows a change or a reset, and keep informing; the document itself is
// checked in adapters/unifi and internal/server.
func TestAccessPointSettings(t *testing.T) {
	d := startDaemon(t, "BEENFAR_UNIFI_COUNTRY=840")
	f := startFleet(t, d, profiledAP, accessPoint)
	pending := d.pending(t, profiledAP.MAC, accessPoint.MAC)

	wifi := configv1connect.NewWifiNetworkServiceClient(d.client)
	if _, err := wifi.CreateWifiNetwork(callCtx(t), &configv1.CreateWifiNetworkRequest{WifiNetwork: &configv1.WifiNetwork{
		Ssid: "e2e-settings", Security: configv1.WifiSecurity_WIFI_SECURITY_WPA_PERSONAL, SecurityKey: "correct horse battery",
	}}); err != nil {
		t.Fatal(err)
	}
	d.adopt(t, pending[profiledAP.MAC])
	d.adopt(t, pending[accessPoint.MAC])
	connected(t, d, f, profiledAP.MAC)
	connected(t, d, f, accessPoint.MAC)

	lite := pending[profiledAP.MAC].GetId()
	other := pending[accessPoint.MAC].GetId()
	get := func(id string) *configv1.DeviceSettings {
		t.Helper()
		r, err := d.devices.GetDeviceSettings(callCtx(t), &configv1.GetDeviceSettingsRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetSettings()
	}
	s := get(lite)
	if s.GetOriginal()["radio.1.channel"] != "auto" || s.GetCurrent()["radio.1.channel"] != "auto" {
		t.Fatalf("settings of the profiled access point: %v", s)
	}
	if o := get(other); len(o.GetOriginal())+len(o.GetCurrent()) != 0 {
		t.Fatalf("an access point without a profile has settings: %v", o)
	}

	update := func(id string, set map[string]string) (*configv1.UpdateDeviceSettingsResponse, error) {
		return d.devices.UpdateDeviceSettings(callCtx(t), &configv1.UpdateDeviceSettingsRequest{Id: id, Current: set})
	}
	if r, err := update(lite, map[string]string{"radio.1.channel": "6", "radio.2.channel": "36"}); err != nil ||
		r.GetSettings().GetCurrent()["radio.1.channel"] != "6" || r.GetSettings().GetOriginal()["radio.1.channel"] != "auto" {
		t.Fatalf("update: %v, %v", r, err)
	}
	waitLastSeenAdvances(t, d, profiledAP.MAC) // it took the new settings and carries on
	if _, err := update(lite, map[string]string{"radio.1.channel": "not-a-channel"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("bad value: %v, want InvalidArgument", err)
	}
	if _, err := update(lite, map[string]string{"radio.1.cwm.mode": "1"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a model-class key: %v, want InvalidArgument", err)
	}
	if _, err := update(other, map[string]string{"radio.1.channel": "6"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("settings of a model without a profile: %v, want FailedPrecondition", err)
	}
	if get(lite).GetCurrent()["radio.1.channel"] != "6" {
		t.Error("a refused update changed the settings")
	}

	r, err := d.devices.ResetDeviceSettings(callCtx(t), &configv1.ResetDeviceSettingsRequest{Id: lite})
	if err != nil || r.GetSettings().GetCurrent()["radio.1.channel"] != "auto" || r.GetSettings().GetCurrent()["radio.2.channel"] != "auto" {
		t.Fatalf("reset: %v, %v", r, err)
	}
	waitLastSeenAdvances(t, d, profiledAP.MAC)
	waitLastSeenAdvances(t, d, accessPoint.MAC)
	connected(t, d, f, profiledAP.MAC)
}

// lastSeen returns the last_seen_time the daemon reports for the device.
func lastSeen(ctx context.Context, d *daemon, mac string) (time.Time, error) {
	resp, err := d.devices.ListDevices(ctx, &configv1.ListDevicesRequest{})
	if err != nil {
		return time.Time{}, err
	}
	for _, dev := range resp.GetDevices() {
		if dev.GetMac() == mac {
			return dev.GetLastSeenTime().AsTime(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%s is not listed", mac)
}

// waitLastSeenAdvances waits until the daemon's last_seen_time for the device
// moves past what it was when the call began: the device is still informing
// and the daemon still accepts it.
func waitLastSeenAdvances(t *testing.T, d *daemon, mac string) {
	t.Helper()
	first, err := lastSeen(callCtx(t), d, mac)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, mac+" last_seen_time to advance", func(ctx context.Context) error {
		now, err := lastSeen(ctx, d, mac)
		if err != nil {
			return err
		}
		if !now.After(first) {
			return errors.New("not advanced")
		}
		return nil
	})
}

// callTimeout bounds a single API call made outside eventually, so a daemon
// that accepts a request and never answers fails the test instead of hanging
// it until the suite's timeout.
const callTimeout = 15 * time.Second

// callCtx returns a context for one API call, cancelled when the test ends.
func callCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callTimeout)
	t.Cleanup(cancel)
	return ctx
}

// eventually polls check until it returns nil. check gets a context that ends
// at the deadline, and must pass it to every request it makes, so a stalled
// request cannot outlive the wait. Past the deadline the test fails with the
// last real error from check.
func eventually(t *testing.T, timeout time.Duration, what string, check func(ctx context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	var last error
	for {
		err := check(ctx)
		if err == nil {
			return
		}
		if ctx.Err() == nil {
			last = err // not just a casualty of the deadline
		}
		if ctx.Err() != nil {
			if last == nil {
				last = err
			}
			t.Fatalf("waiting for %s: %v", what, last)
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// syncBuffer is a bytes.Buffer safe for a process's stdout and stderr, which
// os/exec copies from separate goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// testWriter sends log output to the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

var _ io.Writer = testWriter{}
