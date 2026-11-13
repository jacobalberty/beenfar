package unifi

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jacobalberty/go-unifi/inform"

	"github.com/jacobalberty/beenfar/api/adapter"
)

// classify says what kind of device sent an inform and how many ports beyond
// its uplink can be configured on it.
//
// UniFi does not send a device type in the inform, so the kind comes from the
// model code, whose prefix names the product line (U7PG2, U6-LR and UAP-AC
// are access points; US8P150 and USW-24 are switches; UDM and UXG are
// gateways). A model that matches nothing is KindUnknown, and the controller
// then treats it as unclassified: it is not adopted automatically and its
// ports are not restricted.
//
// Most access points have no port of their own to configure, so they report
// none. The exceptions are the in-wall models (a code containing "IW"), whose
// built-in switch has two LAN ports on the model checked so far; other in-wall models may differ. A switch reports the length of the
// port_table of its inform when it sends one, else the number in its model
// code (US8 -> 8).
func classify(in *inform.Inform) (adapter.DeviceKind, int) {
	model := strings.ToUpper(in.Model)
	switch {
	case hasAnyPrefix(model, "UGW", "UDM", "UDR", "UXG", "UCG", "UDW", "UXG"):
		return adapter.KindGateway, 0
	case hasAnyPrefix(model, "U7", "U6", "U5", "U2", "UAP", "UAL", "UHD", "UFL", "UBB", "BZ2", "UWB", "E7", "UK"):
		// The inform also carries the name UniFi shows ("AC IW"), which
		// says in-wall even when the code is one we do not know.
		if strings.Contains(model, "IW") || strings.Contains(strings.ToUpper(in.ModelDisplay), "IW") {
			return adapter.KindAccessPoint, 2
		}
		return adapter.KindAccessPoint, 0
	case strings.HasPrefix(model, "US"):
		if n := portTableLen(in.Raw); n > 0 {
			return adapter.KindSwitch, n
		}
		return adapter.KindSwitch, switchPortsFromModel(model)
	}
	return adapter.KindUnknown, 0
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// portTableLen is the number of entries of the inform's port_table, or 0.
func portTableLen(raw json.RawMessage) int {
	var v struct {
		PortTable []json.RawMessage `json:"port_table"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return 0
	}
	return len(v.PortTable)
}

// switchPortsFromModel reads the port count out of a switch model code: the
// first run of digits after the "US" or "USW" prefix (US8P150 -> 8,
// USW-24-POE -> 24). A model without one has no known count.
func switchPortsFromModel(model string) int {
	rest := strings.TrimPrefix(strings.TrimPrefix(model, "US"), "W")
	rest = strings.TrimLeft(rest, "-_")
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	// 16 bits is far more ports than any switch has, and keeps the count a
	// bounded value all the way to the wire.
	n, err := strconv.ParseInt(rest[:end], 10, 16)
	if err != nil {
		return 0
	}
	return int(n)
}
