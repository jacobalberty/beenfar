package adapterhost_test

import (
	"slices"
	"testing"

	"github.com/jacobalberty/beenfar/api/adapter"
	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
)

func TestDesiredConfigFollowsAPGroup(t *testing.T) {
	st := openStore(t)
	fa := newFake("fake")
	reg := newRegistry(t, st, fa)
	svc := core.New(st, reg)
	ctx := t.Context()
	host := adapterhost.NewHost("fake", st)

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
