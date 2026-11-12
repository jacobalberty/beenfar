package sqlite_test

import (
	"slices"
	"testing"

	"github.com/jacobalberty/beenfar/internal/store"
)

func fieldNames(ws []store.FieldWriter) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Field)
	}
	return out
}

func TestFieldWriters(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	n, err := s.CreateNetwork(ctx, store.Network{Name: "iot"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateNetwork(ctx, store.Network{Name: "lan"})
	if err != nil {
		t.Fatal(err)
	}

	tf := store.FieldWriter{Writer: "terraform", Kind: store.FieldWriterAPIToken}
	ui := store.FieldWriter{Writer: "alice", Kind: store.FieldWriterUser}
	if err := s.RecordFieldWriters(ctx, store.FieldResourceNetwork, n.ID, []string{"name", "vlan_id"}, tf); err != nil {
		t.Fatal(err)
	}
	// A later write by someone else replaces the writer of that field only.
	if err := s.RecordFieldWriters(ctx, store.FieldResourceNetwork, n.ID, []string{"vlan_id", "subnet"}, ui); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFieldWriters(ctx, store.FieldResourceNetwork, other.ID, []string{"name"}, ui); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetFieldWriters(ctx, store.FieldResourceNetwork, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"name", "subnet", "vlan_id"}; !slices.Equal(fieldNames(got), want) {
		t.Fatalf("fields = %v, want %v", fieldNames(got), want)
	}
	by := map[string]store.FieldWriter{}
	for _, w := range got {
		by[w.Field] = w
		if w.WriteTime.IsZero() {
			t.Errorf("%s: write time not stamped", w.Field)
		}
	}
	if by["name"].Writer != "terraform" || by["name"].Kind != store.FieldWriterAPIToken ||
		by["vlan_id"].Writer != "alice" || by["vlan_id"].Kind != store.FieldWriterUser ||
		by["subnet"].Writer != "alice" {
		t.Errorf("writers = %+v", by)
	}

	all, err := s.ListFieldWriters(ctx, store.FieldResourceNetwork)
	if err != nil || len(all) != 2 || len(all[n.ID]) != 3 || len(all[other.ID]) != 1 {
		t.Fatalf("list = %+v, %v", all, err)
	}
	// Writers are per kind of resource.
	if none, err := s.ListFieldWriters(ctx, store.FieldResourceWifiNetwork); err != nil || len(none) != 0 {
		t.Fatalf("wifi writers = %+v, %v", none, err)
	}

	wantErr(t, s.RecordFieldWriters(ctx, store.FieldResourceNetwork, n.ID, []string{"name"}, store.FieldWriter{Kind: store.FieldWriterUser}), store.ErrInvalid)
	wantErr(t, s.RecordFieldWriters(ctx, "bogus", n.ID, []string{"name"}, ui), store.ErrInvalid)

	// Deleting the resource removes its rows, and only its rows.
	if err := s.DeleteNetwork(ctx, n.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetFieldWriters(ctx, store.FieldResourceNetwork, n.ID); len(got) != 0 {
		t.Errorf("writers outlived the network: %+v", got)
	}
	if got, _ := s.GetFieldWriters(ctx, store.FieldResourceNetwork, other.ID); len(got) != 1 {
		t.Errorf("another network's writers were removed: %+v", got)
	}
}

// Every kind of resource drops its writers when it is deleted.
func TestFieldWritersGoWithEveryResource(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()
	w := store.FieldWriter{Writer: "alice", Kind: store.FieldWriterUser}

	wifi, err := s.CreateWifiNetwork(ctx, store.WifiNetwork{SSID: "x", Security: store.WifiSecurityOpen, Band: store.WifiBandBoth})
	if err != nil {
		t.Fatal(err)
	}
	radius, err := s.CreateRadiusServer(ctx, store.RadiusServer{Name: "r", Address: "10.0.0.5", AuthPort: 1812, SharedSecret: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	dev, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:02", Adapter: "unifi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdoptDevice(ctx, dev.ID); err != nil {
		t.Fatal(err)
	}
	port, err := s.CreateSwitchPort(ctx, store.SwitchPort{DeviceID: dev.ID, PortIndex: 3, Tagging: store.SwitchPortTaggingAll, PoEMode: store.PoEModeAuto})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		res    store.FieldResource
		id     string
		delete func() error
	}{
		{store.FieldResourceWifiNetwork, wifi.ID, func() error { return s.DeleteWifiNetwork(ctx, wifi.ID, 0) }},
		{store.FieldResourceRadius, radius.ID, func() error { return s.DeleteRadiusServer(ctx, radius.ID, 0) }},
		{store.FieldResourceSwitchPort, port.ID, func() error { return s.DeleteSwitchPort(ctx, port.ID, 0) }},
	} {
		if err := s.RecordFieldWriters(ctx, c.res, c.id, []string{"name"}, w); err != nil {
			t.Fatal(err)
		}
		if err := c.delete(); err != nil {
			t.Fatalf("%s: %v", c.res, err)
		}
		if got, _ := s.GetFieldWriters(ctx, c.res, c.id); len(got) != 0 {
			t.Errorf("%s: writers outlived the resource: %+v", c.res, got)
		}
	}
}
