package unifi

import (
	"github.com/jacobalberty/go-unifi/inform"

	"github.com/jacobalberty/beenfar/api/adapter"
)

// classify says what kind of device sent an inform and how many ports beyond
// its uplink it has. The UniFi model rules live in go-unifi
// (inform.Inform.Classify); this maps its answer onto the adapter SDK's kinds.
// A model go-unifi does not recognise is KindUnknown, which the controller
// treats as unclassified: it is not provisioned and its ports are not restricted.
func classify(in *inform.Inform) (adapter.DeviceKind, int) {
	kind, ports := in.Classify()
	switch kind {
	case inform.KindAccessPoint:
		return adapter.KindAccessPoint, ports
	case inform.KindSwitch:
		return adapter.KindSwitch, ports
	case inform.KindGateway:
		return adapter.KindGateway, ports
	}
	return adapter.KindUnknown, 0
}
