package core

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jacobalberty/beenfar/internal/store"
)

// Every mutable field of a resource needs a setter, or a full replace would
// silently keep its stored value. The setter names must be the API field
// names; this compares them with the struct fields, ignoring case and
// underscores (SSID ~ ssid, APGroups ~ ap_groups).
func checkSetters[T any](t *testing.T, setters fieldSetters[T], readOnly ...string) {
	t.Helper()
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }
	want := map[string]bool{}
	typ := reflect.TypeFor[T]()
	for f := range typ.Fields() {
		want[norm(f.Name)] = true
	}
	for _, r := range append(readOnly, "ID", "Revision", "CreateTime", "UpdateTime") {
		delete(want, norm(r))
	}
	for name := range setters {
		if !want[norm(name)] {
			t.Errorf("%s: setter %q matches no mutable field", typ.Name(), name)
		}
		delete(want, norm(name))
	}
	for f := range want {
		t.Errorf("%s: mutable field %q has no update_mask setter", typ.Name(), f)
	}
}

func TestFieldSettersCoverMutableFields(t *testing.T) {
	checkSetters(t, wifiFields)
	checkSetters(t, networkFields)
	checkSetters(t, radiusFields, "SecretVersion")
	checkSetters(t, switchPortFields, "DeviceID", "PortIndex")
}

func TestFieldSettersApply(t *testing.T) {
	dst := store.Network{Name: "a", VLANID: 1, DomainName: "x"}
	if err := networkFields.apply(&dst, store.Network{Name: "b", VLANID: 2}, []string{"vlan_id"}); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "a" || dst.VLANID != 2 || dst.DomainName != "x" {
		t.Fatalf("masked: %+v", dst)
	}
	for _, full := range [][]string{nil, {"*"}} {
		dst := store.Network{Name: "a", DomainName: "x"}
		if err := networkFields.apply(&dst, store.Network{Name: "b"}, full); err != nil {
			t.Fatal(err)
		}
		if dst.Name != "b" || dst.DomainName != "" {
			t.Fatalf("full %v: %+v", full, dst)
		}
	}
	if err := networkFields.apply(&dst, store.Network{}, []string{"name", "nope"}); err == nil {
		t.Fatal("unknown path accepted")
	}
}
