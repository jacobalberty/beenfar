package unifi

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/jacobalberty/go-unifi/inform"

	"github.com/jacobalberty/beenfar/api/adapter"
)

// renderSystemConfig turns the controller's desired configuration into the
// system_cfg document of a SetParam reply.
//
// This is the only place that knows how networks, wifi networks and RADIUS
// servers map to device settings. Every key it writes appears, with a value
// of the same form, in a system.cfg captured from a dual-radio access point
// (madwifi driver: phy wifi0 is 2.4 GHz, wifi1 is 5 GHz) running a WPA2/3
// enterprise SSID with RADIUS authentication and accounting, a WPA-PSK SSID
// and a guest SSID on VLAN 5. testdata/system-cfg-keys.txt lists the
// captured key names, and the tests check every rendered key against it.
//
// The document is a fragment: it holds the WLAN, VLAN, bridge and RADIUS
// sections and nothing for the settings beenfar does not model (radio
// channels and power, SSH users, NTP, syslog, ...). Whether a device merges a
// fragment into its configuration or replaces its configuration with it is
// not verified; the adapter sends it to every adopted access point anyway.
//
// Not rendered, because the capture does not show how:
//   - open and WEP networks, and guest networks (whose client isolation
//     needs ebtables rules): these are reported as errors;
//   - more than one RADIUS server per network;
//   - removing WLANs: an access point with no networks to run is sent no
//     settings, so networks it already runs keep running;
//   - switch ports: the capture only has switch.port.1.name and opmode.
//     DesiredConfig.SwitchPorts is ignored.
func renderSystemConfig(desired adapter.DesiredConfig) (*inform.Config, error) {
	r := renderer{
		desired:  desired,
		keys:     map[string]string{},
		networks: map[string]adapter.Network{},
		radius:   map[string]adapter.RadiusServer{},
		bridges:  map[int][]string{},
	}
	for _, n := range desired.Networks {
		r.networks[n.ID] = n
	}
	for _, s := range desired.RadiusServers {
		r.radius[s.ID] = s
	}
	if err := r.render(); err != nil {
		return nil, err
	}
	return r.config()
}

// radio is one physical radio of the device family the capture comes from.
type radio struct {
	index int    // radio.N
	phy   string // wifi0, wifi1
	band  adapter.WifiBand
	// Per-radio values the capture shows for user SSIDs.
	pureg, dtim string
}

var radios = []radio{
	{index: 1, phy: "wifi0", band: adapter.Band2G, pureg: "0", dtim: "1"},
	{index: 2, phy: "wifi1", band: adapter.Band5G, pureg: "1", dtim: "3"},
}

// vap is one SSID on one radio: a virtual interface such as wifi0ap0.
type vap struct {
	n      int // aaa.n and wireless.n
	dev    string
	radio  radio
	bridge string // br0 or br0.<vlan>
}

type renderer struct {
	desired  adapter.DesiredConfig
	keys     map[string]string
	networks map[string]adapter.Network
	radius   map[string]adapter.RadiusServer
	// bridges maps a VLAN id (0: untagged) to the VAPs bridged onto it.
	bridges map[int][]string
	vaps    []vap
}

func (r *renderer) set(key, value string) { r.keys[key] = value }

func (r *renderer) render() error {
	if len(r.desired.Wifi) == 0 {
		return nil
	}
	wifi := slices.Clone(r.desired.Wifi)
	slices.SortFunc(wifi, func(a, b adapter.WifiNetwork) int { return cmp.Compare(a.SSID, b.SSID) })
	r.bridges[0] = nil

	n := 0
	for _, w := range wifi {
		vlan, err := r.vlanOf(w)
		if err != nil {
			return err
		}
		for _, rad := range radios {
			if w.Band != adapter.BandBoth && w.Band != "" && w.Band != rad.band {
				continue
			}
			n++
			v := vap{n: n, dev: rad.phy + "ap" + strconv.Itoa(n-1), radio: rad, bridge: bridgeDev(vlan)}
			if err := r.renderVAP(v, w); err != nil {
				return err
			}
			r.bridges[vlan] = append(r.bridges[vlan], v.dev)
			r.vaps = append(r.vaps, v)
		}
	}
	r.renderRadios()
	r.renderNetworking()
	return nil
}

func (r *renderer) vlanOf(w adapter.WifiNetwork) (int, error) {
	if w.NetworkID == "" {
		return 0, nil
	}
	n, ok := r.networks[w.NetworkID]
	if !ok {
		return 0, fmt.Errorf("wifi network %q: network %s is not in the configuration", w.SSID, w.NetworkID)
	}
	return n.VLANID, nil
}

func bridgeDev(vlan int) string {
	if vlan == 0 {
		return "br0"
	}
	return "br0." + strconv.Itoa(vlan)
}

func (r *renderer) renderVAP(v vap, w adapter.WifiNetwork) error {
	if w.Guest {
		return fmt.Errorf("wifi network %q: guest networks are not supported on UniFi devices yet (client isolation is not verified)", w.SSID)
	}
	aaa := "aaa." + strconv.Itoa(v.n) + "."
	wl := "wireless." + strconv.Itoa(v.n) + "."
	hidden := strconv.FormatBool(w.Hidden)

	// Settings every SSID in the capture carries.
	for k, val := range map[string]string{
		"11k.status":           "disabled",
		"br.devname":           v.bridge,
		"bss_transition":       "enabled",
		"country_beacon":       "disabled",
		"devname":              v.dev,
		"driver":               "madwifi",
		"eapol_version":        "2",
		"ft.status":            "disabled",
		"hide_ssid":            hidden,
		"id":                   w.ID,
		"is_guest":             "false",
		"p2p":                  "disabled",
		"p2p_cross_connect":    "disabled",
		"pmf.mode":             "0",
		"pmf.status":           "disabled",
		"proxy_arp":            "disabled",
		"radius.macacl.status": "disabled",
		"ssid":                 w.SSID,
		"status":               "enabled",
		"tdls_prohibit":        "disabled",
		"verbose":              "2",
		"wpa":                  "2",
		"wpa.1.pairwise":       "CCMP",
	} {
		r.set(aaa+k, val)
	}
	switch w.Security {
	case adapter.WifiWPAPersonal:
		r.set(aaa+"wpa.key.1.mgmt", "WPA-PSK")
		r.set(aaa+"wpa.psk", w.SecurityKey)
		r.set(aaa+"wpa.group_rekey", "0")
	case adapter.WifiWPAEnterprise:
		srv, ok := r.radius[w.RadiusServerID]
		if !ok {
			return fmt.Errorf("wifi network %q: WPA enterprise needs a RADIUS server (radius_server_id) to be configured on UniFi devices", w.SSID)
		}
		r.set(aaa+"wpa.key.1.mgmt", "WPA-EAP")
		r.set(aaa+"wpa.group_rekey", "3600")
		r.set(aaa+"auth_cache", "enabled")
		r.set(aaa+"dynamic_vlan", "0")
		r.set(aaa+"radius.auth.1.ip", srv.Address)
		r.set(aaa+"radius.auth.1.port", strconv.Itoa(srv.AuthPort))
		r.set(aaa+"radius.auth.1.secret", srv.SharedSecret)
		if srv.AccountingPort != 0 {
			r.set(aaa+"radius.acct.1.ip", srv.Address)
			r.set(aaa+"radius.acct.1.port", strconv.Itoa(srv.AccountingPort))
			r.set(aaa+"radius.acct.1.secret", srv.SharedSecret)
		}
	default:
		return fmt.Errorf("wifi network %q: %s security is not supported on UniFi devices yet", w.SSID, w.Security)
	}

	for k, val := range map[string]string{
		"addmtikie":        "disabled",
		"authmode":         "1",
		"autowds":          "disabled",
		"bga_filter":       "enabled",
		"devname":          v.dev,
		"dtim_period":      v.radio.dtim,
		"element_adopt":    "disabled",
		"hide_ssid":        hidden,
		"id":               w.ID,
		"is_guest":         "false",
		"l2_isolation":     "disabled",
		"mac_acl.policy":   "deny",
		"mac_acl.status":   "enabled",
		"mcast.enhance":    "0",
		"mcastrate":        "auto",
		"mode":             "master",
		"no2ghz_oui":       "disabled",
		"parent":           v.radio.phy,
		"pureg":            v.radio.pureg,
		"puren":            "0",
		"schedule_enabled": "disabled",
		"security":         "none",
		"ssid":             w.SSID,
		"status":           "enabled",
		"uapsd":            "disabled",
		"usage":            "user",
		"vport":            "disabled",
		"vwire":            "disabled",
		"wds":              "disabled",
		"wmm":              "enabled",
	} {
		r.set(wl+k, val)
	}
	return nil
}

// renderRadios attaches the VAPs to their radios: the first one is the
// radio's own devname, the rest are its virtual interfaces.
func (r *renderer) renderRadios() {
	for _, rad := range radios {
		prefix := "radio." + strconv.Itoa(rad.index) + "."
		virtual := 0
		for _, v := range r.vaps {
			if v.radio.phy != rad.phy {
				continue
			}
			if virtual == 0 {
				r.set(prefix+"devname", v.dev)
				r.set(prefix+"phyname", rad.phy)
			} else {
				vp := prefix + "virtual." + strconv.Itoa(virtual) + "."
				r.set(vp+"devname", v.dev)
				r.set(vp+"mode", "master")
				r.set(vp+"status", "enabled")
			}
			virtual++
		}
	}
}

// renderNetworking writes the bridges (br0 for untagged, br0.<vlan> per
// VLAN, each with the matching eth0 interface as uplink), the VLAN
// interfaces, and the interfaces' netconf entries.
func (r *renderer) renderNetworking() {
	vlans := make([]int, 0, len(r.bridges))
	for v := range r.bridges {
		vlans = append(vlans, v)
	}
	slices.Sort(vlans) // 0 (br0) first

	netconf := 0
	addNetconf := func(dev string, up, promisc bool) {
		netconf++
		p := "netconf." + strconv.Itoa(netconf) + "."
		r.set(p+"autoip.status", "disabled")
		r.set(p+"devname", dev)
		r.set(p+"ip", "0.0.0.0")
		r.set(p+"status", "enabled")
		r.set(p+"up", enabledOrDisabled(up))
		if promisc {
			r.set(p+"promisc", "enabled")
		}
	}

	addNetconf("br0", true, false)
	addNetconf("eth0", true, true)
	for i, vlan := range vlans {
		b := "bridge." + strconv.Itoa(i+1) + "."
		dev, uplink := bridgeDev(vlan), "eth0"
		if vlan != 0 {
			uplink = "eth0." + strconv.Itoa(vlan)
			vl := "vlan." + strconv.Itoa(i) + "."
			r.set(vl+"devname", "eth0")
			r.set(vl+"id", strconv.Itoa(vlan))
			addNetconf(dev, true, false)
			addNetconf(uplink, true, false)
		}
		r.set(b+"devname", dev)
		r.set(b+"fd", "1")
		r.set(b+"stp.status", "disabled")
		r.set(b+"port.1.devname", uplink)
		for j, v := range r.bridges[vlan] {
			r.set(b+"port."+strconv.Itoa(j+2)+".devname", v)
		}
	}
	for _, v := range r.vaps {
		addNetconf(v.dev, false, true)
	}

	// The management address comes from DHCP on br0, as in the capture.
	r.set("dhcpc.1.devname", "br0")
	r.set("dhcpc.1.status", "enabled")
	for _, section := range []string{"aaa", "bridge", "dhcpc", "netconf", "wireless"} {
		r.set(section+".status", "enabled")
	}
	if len(vlans) > 1 {
		r.set("vlan.status", "enabled")
	}
}

func enabledOrDisabled(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

// config writes the keys in sorted order, as the device lists them.
func (r *renderer) config() (*inform.Config, error) {
	keys := make([]string, 0, len(r.keys))
	for k := range r.keys {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	c := inform.NewConfig()
	for _, k := range keys {
		if err := c.Set(k, r.keys[k]); err != nil {
			return nil, err
		}
	}
	return c, nil
}
