package adapterhost_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jacobalberty/beenfar/api/adapter"
	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
)

func autoAdoptRegistry(t *testing.T, st store.Store, fa *fake) (*adapterhost.Registry, adapter.Host) {
	t.Helper()
	reg, err := adapterhost.NewRegistry(adapterhost.Options{Store: st, AutoAdopt: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.AddBuiltin(fa); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reg.Start(ctx)
	t.Cleanup(func() { cancel(); reg.Wait() })
	return reg, reg.NewHost("fake")
}

func waitState(t *testing.T, st store.Store, mac string, want store.DeviceState) store.Device {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		d, err := st.GetDeviceByMAC(t.Context(), mac)
		if err == nil && d.State == want {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("device %s: state = %v (%v), want %s", mac, d.State, err, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAutoAdoptAdoptsOnlyAccessPoints(t *testing.T) {
	st := openStore(t)
	fa := newFake("fake")
	_, host := autoAdoptRegistry(t, st, fa)
	ctx := t.Context()

	for _, d := range []adapter.Discovered{
		{MAC: "de:ad:be:ef:01:01", Kind: adapter.KindSwitch, PortCount: 8},
		{MAC: "de:ad:be:ef:01:02"}, // unclassified
		{MAC: "de:ad:be:ef:01:03", Kind: adapter.KindAccessPoint},
		{MAC: "de:ad:be:ef:01:04", Kind: adapter.KindAccessPoint, Locked: true},
	} {
		if _, err := host.ReportDevice(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	ap := waitState(t, st, "de:ad:be:ef:01:03", store.DeviceStateAdopted)
	if ap.Kind != store.DeviceKindAccessPoint || ap.PortCount != 0 {
		t.Fatalf("access point = %+v", ap)
	}
	// Seeing it again must not adopt it a second time.
	if _, err := host.ReportDevice(ctx, adapter.Discovered{MAC: "de:ad:be:ef:01:03", Kind: adapter.KindAccessPoint}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	for _, mac := range []string{"de:ad:be:ef:01:01", "de:ad:be:ef:01:02", "de:ad:be:ef:01:04"} {
		if d, _ := st.GetDeviceByMAC(ctx, mac); d.State != store.DeviceStatePending {
			t.Errorf("%s was adopted: %+v", mac, d)
		}
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	if len(fa.adopted) != 1 || fa.adopted[0].MAC != "de:ad:be:ef:01:03" {
		t.Fatalf("adapter was told to adopt %+v", fa.adopted)
	}
}

func TestNoAutoAdoptByDefault(t *testing.T) {
	st := openStore(t)
	fa := newFake("fake")
	reg := newRegistry(t, st, fa)
	if _, err := reg.NewHost("fake").ReportDevice(t.Context(), adapter.Discovered{MAC: "de:ad:be:ef:01:05", Kind: adapter.KindAccessPoint}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if d, _ := st.GetDeviceByMAC(t.Context(), "de:ad:be:ef:01:05"); d.State != store.DeviceStatePending {
		t.Fatalf("adopted without AutoAdopt: %+v", d)
	}
}

func TestDesiredConfigFollowsAPGroup(t *testing.T) {
	st := openStore(t)
	fa := newFake("fake")
	reg := newRegistry(t, st, fa)
	svc := core.New(st, reg)
	ctx := t.Context()
	host := reg.NewHost("fake")

	for _, w := range []store.WifiNetwork{
		{SSID: "everywhere", Security: store.WifiSecurityWPAPersonal, SecurityKey: "12345678"},
		{SSID: "up", Security: store.WifiSecurityWPAPersonal, SecurityKey: "12345678", APGroups: []string{"upstairs"}},
		{SSID: "down", Security: store.WifiSecurityWPAPersonal, SecurityKey: "12345678", APGroups: []string{"downstairs", "garage"}},
	} {
		if _, err := svc.Wifi.Create(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	d, err := host.ReportDevice(ctx, adapter.Discovered{MAC: "de:ad:be:ef:01:06", Kind: adapter.KindAccessPoint})
	if err != nil {
		t.Fatal(err)
	}
	ssids := func() []string {
		t.Helper()
		cfg, err := host.DesiredConfig(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, w := range cfg.Wifi {
			out = append(out, w.SSID)
		}
		slices.Sort(out)
		return out
	}
	rev := func() int64 {
		cfg, _ := host.DesiredConfig(ctx, d.ID)
		return cfg.Revision
	}

	if got := ssids(); !slices.Equal(got, []string{"down", "everywhere", "up"}) {
		t.Fatalf("ungrouped access point gets %v", got)
	}
	before := rev()
	if _, err := svc.Devices.Update(ctx, store.Device{ID: d.ID, APGroup: "garage"}, []string{"ap_group"}); err != nil {
		t.Fatal(err)
	}
	if got := ssids(); !slices.Equal(got, []string{"down", "everywhere"}) {
		t.Fatalf("access point in garage gets %v", got)
	}
	if rev() == before {
		t.Fatal("moving the access point to another group did not change the configuration revision")
	}
	if _, err := svc.Devices.Update(ctx, store.Device{ID: d.ID}, nil); err != nil { // empty mask: replace all
		t.Fatal(err)
	}
	if got := ssids(); !slices.Equal(got, []string{"down", "everywhere", "up"}) {
		t.Fatalf("after clearing the group the access point gets %v", got)
	}
}
