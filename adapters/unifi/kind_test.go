package unifi

import (
	"testing"

	"github.com/jacobalberty/go-unifi/inform"

	"github.com/jacobalberty/beenfar/api/adapter"
)

// The model rules are tested in go-unifi; this checks the mapping.
func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		model string
		kind  adapter.DeviceKind
		ports int
	}{
		{"U7PG2", adapter.KindAccessPoint, 0},
		{"UAP-AC-IW", adapter.KindAccessPoint, 2},
		{"US8P150", adapter.KindSwitch, 8},
		{"UDMPRO", adapter.KindGateway, 0},
		{"mystery", adapter.KindUnknown, 0},
	} {
		kind, ports := classify(&inform.Inform{Model: tc.model})
		if kind != tc.kind || ports != tc.ports {
			t.Errorf("classify(%q) = %q, %d; want %q, %d", tc.model, kind, ports, tc.kind, tc.ports)
		}
	}
}
