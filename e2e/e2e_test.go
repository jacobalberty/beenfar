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
	eventually(t, 30*time.Second, "beenfard to serve /healthz", func() error {
		select {
		case <-exited:
			return fmt.Errorf("beenfard exited early: %v\n%s", waitErr, logs.String())
		default:
		}
		resp, err := http.Get(url + "/healthz")
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
}

// startFleet starts the given devices informing d once a second.
func startFleet(t *testing.T, d *daemon, specs ...emu.DeviceSpec) *fleet {
	t.Helper()
	// The emulator logs through the standard logger; keep that in the test log.
	log.SetOutput(testWriter{t})
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	e := emu.New(d.url+"/inform", emu.WithInformInterval(time.Second))
	if err := e.Add(specs...); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	return &fleet{Emu: e, specs: specs}
}

// The devices the tests use. MACs are fixed so failures are reproducible; the
// models are three different device types, so both the access point and the
// non-access-point paths run.
var (
	accessPoint = emu.DeviceSpec{MAC: "00:27:22:e0:00:01", Model: "U7PRO", Name: "e2e-ap"}
	switchDev   = emu.DeviceSpec{MAC: "00:27:22:e0:00:02", Model: "USM8P", Name: "e2e-switch"}
	gateway     = emu.DeviceSpec{MAC: "00:27:22:e0:00:03", Model: "UGW3", Name: "e2e-gateway"}
)

// pending waits until the daemon lists exactly the devices with the given
// MACs as pending, and returns them by MAC.
func (d *daemon) pending(t *testing.T, macs ...string) map[string]*configv1.Device {
	t.Helper()
	var got map[string]*configv1.Device
	eventually(t, 30*time.Second, fmt.Sprintf("%d pending devices", len(macs)), func() error {
		resp, err := d.devices.ListPendingDevices(t.Context(), &configv1.ListPendingDevicesRequest{})
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
	_, err := d.devices.AdoptDevice(t.Context(), &configv1.AdoptDeviceRequest{
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
	eventually(t, 15*time.Second, mac+" to be adopted in the daemon", func() error {
		st, err := d.state(t.Context(), mac)
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
	first := lastSeen(t, d, accessPoint.MAC)
	eventually(t, 15*time.Second, "last_seen_time to advance", func() error {
		if now := lastSeen(t, d, accessPoint.MAC); !now.After(first) {
			return errors.New("not advanced")
		}
		return nil
	})
}

// With AES-GCM switched on, devices adopted afterwards are told to move to it
// and carry on talking to the daemon through the switch.
func TestAdoptionWithAESGCM(t *testing.T) {
	d := startDaemon(t, "BEENFAR_UNIFI_GCM=true")
	f := startFleet(t, d, accessPoint, switchDev)

	pending := d.pending(t, accessPoint.MAC, switchDev.MAC)
	for _, spec := range f.specs {
		d.adopt(t, pending[spec.MAC])
	}
	for _, spec := range f.specs {
		connected(t, d, f, spec.MAC)
	}
	// Informs after the switch must still decode on both sides.
	first := lastSeen(t, d, switchDev.MAC)
	eventually(t, 15*time.Second, "informs to continue after the GCM switch", func() error {
		if !lastSeen(t, d, switchDev.MAC).After(first) {
			return errors.New("last_seen_time not advancing")
		}
		return nil
	})
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

	adopted, err := d.devices.ListDevices(t.Context(), &configv1.ListDevicesRequest{State: configv1.DeviceState_DEVICE_STATE_ADOPTED})
	if err != nil || len(adopted.GetDevices()) != 1 {
		t.Fatalf("adopted devices = %v, %v", adopted, err)
	}
	if _, err := d.devices.ForgetDevice(t.Context(), &configv1.ForgetDeviceRequest{Id: adopted.GetDevices()[0].GetId()}); err != nil {
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

func lastSeen(t *testing.T, d *daemon, mac string) time.Time {
	t.Helper()
	resp, err := d.devices.ListDevices(t.Context(), &configv1.ListDevicesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, dev := range resp.GetDevices() {
		if dev.GetMac() == mac {
			return dev.GetLastSeenTime().AsTime()
		}
	}
	t.Fatalf("%s is not listed", mac)
	return time.Time{}
}

// eventually polls check until it returns nil, failing the test with the last
// error once timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, check func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return
		}
		if time.Now().After(deadline) || t.Context().Err() != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
		time.Sleep(100 * time.Millisecond)
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
