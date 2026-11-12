package wire_test

import (
	"reflect"
	"testing"

	"github.com/jacobalberty/beenfar/api/adapter/model"
	wiregen "github.com/jacobalberty/beenfar/api/adapter/wire/gen"
)

// A desired configuration survives the trip to an external adapter.
func TestDesiredConfigRoundTrip(t *testing.T) {
	in := model.DesiredConfig{
		Revision: 42,
		Networks: []model.Network{{ID: "n1", Name: "iot", VLANID: 20, Subnet: "10.0.20.1/24"}},
		Wifi: []model.WifiNetwork{{
			ID: "w1", SSID: "corp", Security: model.WifiWPAEnterprise, Band: model.BandBoth,
			NetworkID: "n1", RadiusServerID: "r1",
		}},
		RadiusServers: []model.RadiusServer{{ID: "r1", Address: "10.0.0.2", AuthPort: 1812, AccountingPort: 1813, SharedSecret: "s"}},
		SwitchPorts: []model.SwitchPort{
			{PortIndex: 1, Name: "uplink", Tagging: model.TaggingAll, PoEMode: model.PoEOff, TaggedNetworkIDs: []string{}},
			{PortIndex: 2, Disabled: true, NativeNetworkID: "n1", Tagging: model.TaggingCustom, TaggedNetworkIDs: []string{"n1"}, PoEMode: model.PoEAuto},
		},
	}
	out := wiregen.DesiredConfigFromProto(wiregen.DesiredConfigToProto(in))
	if out.SwitchPorts[0].TaggedNetworkIDs == nil {
		out.SwitchPorts[0].TaggedNetworkIDs = []string{}
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip:\n in  %+v\n out %+v", in, out)
	}
}
