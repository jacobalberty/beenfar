package adapterhost_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jacobalberty/beenfar/api/adapter"
	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
)

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "beenfar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// fake is a built-in adapter that records the commands it gets.
type fake struct {
	name      string
	started   chan adapter.Host
	adoptErr  error
	mu        sync.Mutex
	adopted   []adapter.Device
	forgotten []adapter.Device
}

func newFake(name string) *fake { return &fake{name: name, started: make(chan adapter.Host, 1)} }

func (f *fake) Name() string { return f.name }
func (f *fake) Start(ctx context.Context, h adapter.Host) error {
	f.started <- h
	<-ctx.Done()
	return ctx.Err()
}
func (f *fake) Adopt(_ context.Context, d adapter.Device) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adopted = append(f.adopted, d)
	return f.adoptErr
}
func (f *fake) Forget(_ context.Context, d adapter.Device) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotten = append(f.forgotten, d)
	return nil
}
func (f *fake) HTTPHandlers() map[string]http.Handler {
	return map[string]http.Handler{"POST /" + f.name: http.NotFoundHandler()}
}

func newRegistry(t *testing.T, st store.Store, adapters ...adapter.Adapter) *adapterhost.Registry {
	t.Helper()
	reg, err := adapterhost.NewRegistry(adapterhost.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range adapters {
		if err := reg.AddBuiltin(a); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	reg.Start(ctx)
	t.Cleanup(func() { cancel(); reg.Wait() })
	return reg
}

func TestRegistryListsAndRoutes(t *testing.T) {
	st := openStore(t)
	fa := newFake("fake")
	reg := newRegistry(t, st, fa)

	list := reg.ListAdapters(t.Context())
	if len(list) != 1 || list[0].Name != "fake" || list[0].Kind != core.AdapterBuiltin || !list[0].Healthy || list[0].ConnectTime.IsZero() {
		t.Fatalf("list = %+v", list)
	}
	if _, ok := reg.HTTPHandlers()["POST /fake"]; !ok {
		t.Fatalf("handlers = %v", reg.HTTPHandlers())
	}
	if err := reg.AddBuiltin(newFake("fake")); err == nil {
		t.Fatal("duplicate adapter name accepted")
	}

	d, err := st.ReportDevice(t.Context(), store.DiscoveredDevice{MAC: "de:ad:be:ef:00:01", Adapter: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.AdoptDevice(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if err := reg.ForgetDevice(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if len(fa.adopted) != 1 || fa.adopted[0].ID != d.ID || len(fa.forgotten) != 1 {
		t.Fatalf("adopted %v forgotten %v", fa.adopted, fa.forgotten)
	}

	d.Adapter = "other"
	if err := reg.AdoptDevice(t.Context(), d); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("adopt on unknown adapter = %v", err)
	}
}

func TestStoppedAdapterIsUnhealthy(t *testing.T) {
	st := openStore(t)
	reg, err := adapterhost.NewRegistry(adapterhost.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.AddBuiltin(newFake("fake")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reg.Start(ctx)
	cancel()
	reg.Wait()
	if l := reg.ListAdapters(t.Context()); len(l) != 1 || l[0].Healthy {
		t.Fatalf("list = %+v", l)
	}
	d := store.Device{ID: "x", Adapter: "fake"}
	if err := reg.AdoptDevice(t.Context(), d); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("adopt on stopped adapter = %v", err)
	}
}

func TestHost(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	fa := newFake("fake")
	newRegistry(t, st, fa)
	host := <-fa.started

	d, err := host.ReportDevice(ctx, adapter.Discovered{MAC: "DE-AD-BE-EF-00-02", Model: "U6-Lite", IP: "10.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	if d.MAC != "de:ad:be:ef:00:02" || d.State != adapter.DevicePending {
		t.Fatalf("device = %+v", d)
	}
	if got, err := host.DeviceByMAC(ctx, "deadbeef0002"); err != nil || got.ID != d.ID {
		t.Fatalf("DeviceByMAC = %+v, %v", got, err)
	}
	if _, err := host.DeviceByMAC(ctx, "deadbeef0003"); !errors.Is(err, adapter.ErrNotFound) {
		t.Fatalf("DeviceByMAC unknown = %v", err)
	}

	// Locked devices are reported as such, and clear when reported again.
	if _, err := host.ReportDevice(ctx, adapter.Discovered{MAC: "de:ad:be:ef:00:02", Locked: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetDevice(ctx, d.ID); !got.Locked {
		t.Fatal("device not locked")
	}

	// Secrets: set, read, replace, remove.
	if _, err := host.DeviceSecret(ctx, d.ID, "current"); !errors.Is(err, adapter.ErrNoSecret) {
		t.Fatalf("missing secret = %v", err)
	}
	if err := host.SetDeviceSecret(ctx, d.ID, "current", []byte("k1")); err != nil {
		t.Fatal(err)
	}
	if got, err := host.DeviceSecret(ctx, d.ID, "current"); err != nil || string(got) != "k1" {
		t.Fatalf("secret = %q, %v", got, err)
	}
	if err := host.SetDeviceSecret(ctx, d.ID, "current", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := host.DeviceSecret(ctx, d.ID, "current"); !errors.Is(err, adapter.ErrNoSecret) {
		t.Fatalf("removed secret = %v", err)
	}

	// State reports update the device and unlock it.
	if err := host.ReportState(ctx, d.ID, adapter.State{Firmware: "6.5.1", ConfigRevision: 7}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetDevice(ctx, d.ID)
	if got.Locked || got.Firmware != "6.5.1" || got.ConfigRevision != 7 {
		t.Fatalf("device = %+v", got)
	}
}

// An adapter cannot see or change another adapter's devices.
func TestHostIsScopedToItsAdapter(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	a, b := newFake("a"), newFake("b")
	newRegistry(t, st, a, b)
	ha, hb := <-a.started, <-b.started

	d, err := ha.ReportDevice(ctx, adapter.Discovered{MAC: "de:ad:be:ef:00:04"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hb.ReportDevice(ctx, adapter.Discovered{MAC: "de:ad:be:ef:00:04"}); !errors.Is(err, adapter.ErrNotFound) {
		t.Fatalf("other adapter reported our device: %v", err)
	}
	if got, _ := st.GetDevice(ctx, d.ID); got.Adapter != "a" {
		t.Fatalf("device moved to %q", got.Adapter)
	}
	if _, err := hb.DeviceByMAC(ctx, "de:ad:be:ef:00:04"); !errors.Is(err, adapter.ErrNotFound) {
		t.Fatalf("DeviceByMAC = %v", err)
	}
	if _, err := hb.DeviceSecret(ctx, d.ID, "x"); !errors.Is(err, adapter.ErrNotFound) {
		t.Fatalf("DeviceSecret = %v", err)
	}
	if err := hb.SetDeviceSecret(ctx, d.ID, "x", []byte("y")); !errors.Is(err, adapter.ErrNotFound) {
		t.Fatalf("SetDeviceSecret = %v", err)
	}
	if _, err := hb.DesiredConfig(ctx, d.ID); !errors.Is(err, adapter.ErrNotFound) {
		t.Fatalf("DesiredConfig = %v", err)
	}
}

func TestDesiredConfigRevisionTracksChanges(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	fa := newFake("fake")
	newRegistry(t, st, fa)
	host := <-fa.started
	d, err := host.ReportDevice(ctx, adapter.Discovered{MAC: "de:ad:be:ef:00:05"})
	if err != nil {
		t.Fatal(err)
	}

	empty, err := host.DesiredConfig(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Wifi) != 0 || len(empty.Networks) != 0 || empty.Revision <= 0 {
		t.Fatalf("empty config = %+v", empty)
	}
	again, _ := host.DesiredConfig(ctx, d.ID)
	if again.Revision != empty.Revision {
		t.Fatal("revision changed without a change")
	}

	w, err := st.CreateWifiNetwork(ctx, store.WifiNetwork{SSID: "home", Security: store.WifiSecurityWPAPersonal, SecurityKey: "hunter2hunter2", Band: store.WifiBandBoth})
	if err != nil {
		t.Fatal(err)
	}
	withWifi, _ := host.DesiredConfig(ctx, d.ID)
	if withWifi.Revision == empty.Revision || len(withWifi.Wifi) != 1 || withWifi.Wifi[0].SSID != "home" {
		t.Fatalf("config = %+v", withWifi)
	}
	w.SecurityKey = "another-passphrase"
	if _, err := st.UpdateWifiNetwork(ctx, w); err != nil {
		t.Fatal(err)
	}
	updated, _ := host.DesiredConfig(ctx, d.ID)
	if updated.Revision == withWifi.Revision {
		t.Fatal("revision did not change after an update")
	}
	if err := st.DeleteWifiNetwork(ctx, w.ID, 0); err != nil {
		t.Fatal(err)
	}
	if gone, _ := host.DesiredConfig(ctx, d.ID); gone.Revision != empty.Revision {
		t.Fatal("revision after deleting everything should match the empty one")
	}
}
