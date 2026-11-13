package unifi

import (
	"testing"

	"github.com/jacobalberty/go-unifi/inform"

	"github.com/jacobalberty/beenfar/api/adapter"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		model   string
		raw     string
		kind    adapter.DeviceKind
		ports   int
		display string
	}{
		{"U7PG2", "", adapter.KindAccessPoint, 0, ""},
		{"U6LR", "", adapter.KindAccessPoint, 0, ""},
		{"UAP-AC-Pro", "", adapter.KindAccessPoint, 0, ""},
		{"U6IW", "", adapter.KindAccessPoint, 2, ""},
		{"U7XX", "", adapter.KindAccessPoint, 2, "AC IW"},
		{"UAP-AC-IW", "", adapter.KindAccessPoint, 2, ""},
		{"US8P150", "", adapter.KindSwitch, 8, ""},
		{"USW-24-POE", "", adapter.KindSwitch, 24, ""},
		{"USMINI", "", adapter.KindSwitch, 0, ""},
		{"US16P150", `{"port_table":[{},{},{}]}`, adapter.KindSwitch, 3, ""},
		{"UDMPRO", "", adapter.KindGateway, 0, ""},
		{"mystery", "", adapter.KindUnknown, 0, ""},
		{"", "", adapter.KindUnknown, 0, ""},
	} {
		in := &inform.Inform{Model: tc.model, ModelDisplay: tc.display, Raw: []byte(tc.raw)}
		kind, ports := classify(in)
		if kind != tc.kind || ports != tc.ports {
			t.Errorf("classify(%q) = %q, %d; want %q, %d", tc.model, kind, ports, tc.kind, tc.ports)
		}
	}
}
