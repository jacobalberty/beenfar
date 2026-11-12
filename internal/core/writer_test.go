package core_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
)

func newServices(t *testing.T) *core.Services {
	t.Helper()
	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "beenfar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return core.New(st, nil)
}

func writers(ws []store.FieldWriter) map[string]string {
	out := map[string]string{}
	for _, w := range ws {
		out[w.Field] = w.Writer + "/" + string(w.Kind)
	}
	return out
}

func TestWritersFollowTheMask(t *testing.T) {
	svc := newServices(t)
	base := t.Context()
	tf := core.WithWriter(base, store.FieldWriter{Writer: "terraform-ci", Kind: store.FieldWriterAPIToken})
	alice := core.WithWriter(base, store.FieldWriter{Writer: "alice", Kind: store.FieldWriterUser})

	// A create attributes every field to its creator.
	n, err := svc.Networks.Create(tf, store.Network{Name: "iot", VLANID: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got := writers(n.FieldWriters); len(got) != 7 || got["name"] != "terraform-ci/api_token" || got["dhcp_stop"] != "terraform-ci/api_token" {
		t.Fatalf("after create: %v", got)
	}

	// An update attributes only the masked fields; the rest keep their writer.
	n, err = svc.Networks.Update(alice, store.Network{ID: n.ID, VLANID: 30, DomainName: "iot.lan"}, []string{"vlan_id", "domain_name"})
	if err != nil {
		t.Fatal(err)
	}
	got := writers(n.FieldWriters)
	if got["vlan_id"] != "alice/user" || got["domain_name"] != "alice/user" || got["name"] != "terraform-ci/api_token" || got["subnet"] != "terraform-ci/api_token" {
		t.Fatalf("after masked update: %v", got)
	}

	// Get and List report the same writers.
	g, err := svc.Networks.Get(base, n.ID)
	if err != nil || !slices.Equal(g.FieldWriters, n.FieldWriters) {
		t.Fatalf("get = %+v, %v", g.FieldWriters, err)
	}
	list, err := svc.Networks.List(base)
	if err != nil || len(list) != 1 || !slices.Equal(list[0].FieldWriters, n.FieldWriters) {
		t.Fatalf("list = %+v, %v", list, err)
	}

	// An empty mask replaces everything, so everything is attributed.
	n, err = svc.Networks.Update(alice, store.Network{ID: n.ID, Name: "iot2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for f, w := range writers(n.FieldWriters) {
		if w != "alice/user" {
			t.Errorf("after full replace, %s written by %s", f, w)
		}
	}

	// A rejected update changes nothing, writers included.
	before := n.FieldWriters
	if _, err := svc.Networks.Update(tf, store.Network{ID: n.ID, VLANID: 5000}, []string{"vlan_id"}); err == nil {
		t.Fatal("invalid update accepted")
	}
	if _, err := svc.Networks.Update(tf, store.Network{ID: n.ID, Name: "x", Revision: 99}, []string{"name"}); err == nil {
		t.Fatal("stale update accepted")
	}
	if after, _ := svc.Networks.Get(base, n.ID); !slices.Equal(after.FieldWriters, before) {
		t.Errorf("failed updates changed the writers: %+v", after.FieldWriters)
	}

	// Writes without an identified caller are not attributed, and do not fail.
	n2, err := svc.Networks.Create(base, store.Network{Name: "anon"})
	if err != nil || len(n2.FieldWriters) != 0 {
		t.Fatalf("anonymous create = %+v, %v", n2.FieldWriters, err)
	}
}

func TestWritersOnEveryResource(t *testing.T) {
	svc := newServices(t)
	alice := core.WithWriter(t.Context(), store.FieldWriter{Writer: "alice", Kind: store.FieldWriterUser})

	w, err := svc.Wifi.Create(alice, store.WifiNetwork{SSID: "home", Security: store.WifiSecurityWPAPersonal, SecurityKey: "hunter2hunter2"})
	if err != nil || writers(w.FieldWriters)["ssid"] != "alice/user" {
		t.Fatalf("wifi = %+v, %v", w.FieldWriters, err)
	}
	r, err := svc.Radius.Create(alice, store.RadiusServer{Name: "r", Address: "10.0.0.5", SharedSecret: "s3cret"})
	if err != nil || writers(r.FieldWriters)["shared_secret"] != "alice/user" {
		t.Fatalf("radius = %+v, %v", r.FieldWriters, err)
	}
	bob := core.WithWriter(t.Context(), store.FieldWriter{Writer: "bob", Kind: store.FieldWriterUser})
	r, err = svc.Radius.Update(bob, store.RadiusServer{ID: r.ID, SharedSecret: "new-s3cret"}, []string{"shared_secret"})
	if err != nil || writers(r.FieldWriters)["shared_secret"] != "bob/user" || writers(r.FieldWriters)["address"] != "alice/user" {
		t.Fatalf("radius rotate = %+v, %v", r.FieldWriters, err)
	}
	// The wireless network keeps its own writers: another kind of resource.
	if g, _ := svc.Wifi.Get(t.Context(), w.ID); writers(g.FieldWriters)["ssid"] != "alice/user" || len(g.FieldWriters) != 11 {
		t.Errorf("wifi writers = %+v", g.FieldWriters)
	}
}
