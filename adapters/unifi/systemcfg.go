package unifi

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/jacobalberty/go-unifi/inform/profile"

	"github.com/jacobalberty/beenfar/api/adapter"
)

// errNoDocument means no system_cfg is to be sent, for a reason that is not an
// error of the configuration: the model has no profile, there is nothing to run,
// or the adapter was given no settings.
var errNoDocument = errors.New("no system.cfg is sent")

// SettingsSchema describes the settings an operator can change on a device of
// the model: their defaults and a check for a set of them. ok is false for a
// model with no profile, which has no settings and is sent no system.cfg.
func (a *Adapter) SettingsSchema(model string) (defaults map[string]string, check func(map[string]string) error, ok bool) {
	p, ok := profile.Lookup(model)
	if !ok {
		return nil, nil, false
	}
	return p.Defaults(), p.CheckTunables, true
}

// renderSystemConfig builds the complete system.cfg of a device, which
// replaces the device's whole configuration. It returns an error wrapping
// errNoDocument when nothing should be sent, and another error for a
// configuration that cannot be rendered; the caller sends no system_cfg in
// either case, never a partial document.
//
// A device with no networks to run is sent nothing, though a document with
// none is valid: the profile's base was never observed with zero networks, so
// that document has never been seen on a device. (The consequence is that
// deleting the last network leaves the device running it.)
func renderSystemConfig(model string, desired adapter.DesiredConfig, tunables map[string]string, site SiteSettings, informHost string) (profile.Document, error) {
	p, ok := profile.Lookup(model)
	if !ok {
		return nil, fmt.Errorf("%w: model %q has no profile", errNoDocument, model)
	}
	if len(desired.Wifi) == 0 {
		return nil, fmt.Errorf("%w: the device has no wifi networks to run", errNoDocument)
	}
	ps, err := site.profileSite(informHost)
	if err != nil {
		return nil, err
	}
	radius := map[string]adapter.RadiusServer{}
	for _, r := range desired.RadiusServers {
		radius[r.ID] = r
	}
	vlans := map[string]int{}
	for _, n := range desired.Networks {
		vlans[n.ID] = n.VLANID
	}
	wifi := slices.Clone(desired.Wifi)
	slices.SortFunc(wifi, func(a, b adapter.WifiNetwork) int {
		return cmp.Or(cmp.Compare(a.SSID, b.SSID), cmp.Compare(a.ID, b.ID))
	})
	nets := make([]profile.Network, 0, len(wifi))
	for _, w := range wifi {
		n, err := toNetwork(w, vlans, radius, site.ControllerKey)
		if err != nil {
			return nil, err
		}
		nets = append(nets, n)
	}
	return profile.Render(p, profile.Inputs{Tunables: tunables, Site: ps, Networks: nets})
}

func toNetwork(w adapter.WifiNetwork, vlans map[string]int, radius map[string]adapter.RadiusServer, siteKey string) (profile.Network, error) {
	n := profile.Network{ID: w.ID, SSID: w.SSID, Hidden: w.Hidden, Guest: w.Guest, IAPPKey: iappKey(siteKey, w.ID)}
	if w.NetworkID != "" {
		vlan, ok := vlans[w.NetworkID]
		if !ok {
			return n, fmt.Errorf("wifi network %q: network %s is not in the configuration", w.SSID, w.NetworkID)
		}
		n.VLAN = vlan
	}
	switch w.Band {
	case adapter.BandBoth, "":
	case adapter.Band2G:
		n.Bands = []profile.Band{profile.Band2G}
	case adapter.Band5G:
		n.Bands = []profile.Band{profile.Band5G}
	default:
		return n, fmt.Errorf("wifi network %q: band %q is not supported", w.SSID, w.Band)
	}
	switch w.Security {
	case adapter.WifiWPAPersonal:
		n.Security, n.PSK = profile.WPAPersonal, w.SecurityKey
	case adapter.WifiWPAEnterprise:
		srv, ok := radius[w.RadiusServerID]
		if !ok {
			return n, fmt.Errorf("wifi network %q: WPA enterprise needs a RADIUS server (radius_server_id) to be configured on UniFi devices", w.SSID)
		}
		n.Security = profile.WPAEnterprise
		n.Radius = &profile.Radius{Auth: profile.Server{IP: srv.Address, Port: srv.AuthPort, Secret: srv.SharedSecret}}
		if srv.AccountingPort != 0 {
			n.Radius.Acct = &profile.Server{IP: srv.Address, Port: srv.AccountingPort, Secret: srv.SharedSecret}
		}
	default:
		return n, fmt.Errorf("wifi network %q: %s security is not supported on UniFi devices yet", w.SSID, w.Security)
	}
	return n, nil
}

// iappKey is the per-network secret of the document (aaa.N.iapp_key): 32 hex
// digits derived from the site's controller key and the network's id, so that
// it is stable, differs per network, and is not a constant.
func iappKey(siteKey, networkID string) string {
	m := hmac.New(sha256.New, []byte(siteKey))
	m.Write([]byte("iapp:" + networkID))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// documentRevision is the config revision of a rendered document: a digest of
// every key and value, so it changes exactly when the document does. It is
// positive and never 0 (the revision of "no configuration").
func documentRevision(doc profile.Document) int64 {
	sum := sha256.Sum256([]byte(doc.String()))
	rev := int64(binary.BigEndian.Uint64(sum[:8]) >> 1)
	if rev == 0 {
		rev = 1
	}
	return rev
}
