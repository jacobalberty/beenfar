package unifi

import (
	"github.com/jacobalberty/go-unifi/inform"

	"github.com/jacobalberty/beenfar/api/adapter"
)

// renderSystemConfig turns the controller's desired configuration into the
// system_cfg document of a SetParam reply.
//
// This is the only place that knows how networks and wifi networks map to
// device settings, and it is deliberately empty: the key names the firmware
// expects for WLANs and VLANs have not been verified against real devices
// (neither the go-unifi codec nor its test captures contain them), so none is
// guessed. Until they are known, adopted devices are re-keyed and tracked
// through config revisions, but are sent no wifi or VLAN settings.
//
// To fill it in, set one key per setting on the returned Config and add a
// test with expected output taken from a real device's system.cfg.
func renderSystemConfig(adapter.DesiredConfig) (*inform.Config, error) {
	return inform.NewConfig(), nil
}
