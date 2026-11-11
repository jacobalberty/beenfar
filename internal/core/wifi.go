package core

import (
	"context"
	"slices"

	"github.com/jacobalberty/beenfar/internal/store"
)

// WifiService manages wireless networks.
type WifiService struct {
	store store.Store
}

func (s *WifiService) Create(ctx context.Context, w store.WifiNetwork) (store.WifiNetwork, error) {
	w, err := normalizeWifi(w)
	if err != nil {
		return store.WifiNetwork{}, err
	}
	return s.store.CreateWifiNetwork(ctx, w)
}

func (s *WifiService) Get(ctx context.Context, id string) (store.WifiNetwork, error) {
	return s.store.GetWifiNetwork(ctx, id)
}

func (s *WifiService) List(ctx context.Context) ([]store.WifiNetwork, error) {
	return s.store.ListWifiNetworks(ctx)
}

// wifiFields are the mutable fields of a wireless network, by API name.
var wifiFields = fieldSetters[store.WifiNetwork]{
	"ssid":               func(d *store.WifiNetwork, s store.WifiNetwork) { d.SSID = s.SSID },
	"security":           func(d *store.WifiNetwork, s store.WifiNetwork) { d.Security = s.Security },
	"security_key":       func(d *store.WifiNetwork, s store.WifiNetwork) { d.SecurityKey = s.SecurityKey },
	"band":               func(d *store.WifiNetwork, s store.WifiNetwork) { d.Band = s.Band },
	"network_id":         func(d *store.WifiNetwork, s store.WifiNetwork) { d.NetworkID = s.NetworkID },
	"guest":              func(d *store.WifiNetwork, s store.WifiNetwork) { d.Guest = s.Guest },
	"hidden":             func(d *store.WifiNetwork, s store.WifiNetwork) { d.Hidden = s.Hidden },
	"ap_groups":          func(d *store.WifiNetwork, s store.WifiNetwork) { d.APGroups = s.APGroups },
	"default_user_group": func(d *store.WifiNetwork, s store.WifiNetwork) { d.DefaultUserGroup = s.DefaultUserGroup },
	"radius_profile":     func(d *store.WifiNetwork, s store.WifiNetwork) { d.RadiusProfile = s.RadiusProfile },
	"radius_server_id":   func(d *store.WifiNetwork, s store.WifiNetwork) { d.RadiusServerID = s.RadiusServerID },
}

// Update replaces the fields named by paths (API field names), or every
// mutable field when paths is empty or "*". Fields not named keep their
// stored values. A non-zero w.Revision must match the stored revision.
func (s *WifiService) Update(ctx context.Context, w store.WifiNetwork, paths []string) (store.WifiNetwork, error) {
	return updateMasked(ctx, s.store, w, paths, wifiFields,
		func(w *store.WifiNetwork) *int64 { return &w.Revision },
		func(ctx context.Context, tx store.Store) (store.WifiNetwork, error) {
			return tx.GetWifiNetwork(ctx, w.ID)
		},
		normalizeWifi,
		func(ctx context.Context, tx store.Store, w store.WifiNetwork) (store.WifiNetwork, error) {
			return tx.UpdateWifiNetwork(ctx, w)
		})
}

// Delete deletes by id. A non-zero revision must match.
func (s *WifiService) Delete(ctx context.Context, id string, revision int64) error {
	return s.store.DeleteWifiNetwork(ctx, id, revision)
}

func normalizeWifi(w store.WifiNetwork) (store.WifiNetwork, error) {
	if w.SSID == "" || len(w.SSID) > 32 {
		return w, invalid("ssid must be 1-32 bytes")
	}
	if w.Band == "" {
		w.Band = store.WifiBandBoth
	}
	switch w.Band {
	case store.WifiBandBoth, store.WifiBand2G, store.WifiBand5G:
	default:
		return w, invalid("unknown band %q", w.Band)
	}

	switch w.Security {
	case store.WifiSecurityOpen:
		if w.SecurityKey != "" {
			return w, invalid("security_key must be empty for an open network")
		}
	case store.WifiSecurityWPAPersonal:
		if !validWPAKey(w.SecurityKey) {
			return w, invalid("WPA personal security_key must be 8-63 printable ASCII characters or 64 hex digits")
		}
	case store.WifiSecurityWEP:
		if !validWEPKey(w.SecurityKey) {
			return w, invalid("WEP security_key must be 5 or 13 ASCII characters, or 10 or 26 hex digits")
		}
	case store.WifiSecurityWPAEnterprise:
		if w.SecurityKey != "" {
			return w, invalid("security_key must be empty for WPA enterprise")
		}
		if w.RadiusProfile == "" {
			return w, invalid("radius_profile is required for WPA enterprise")
		}
	case "":
		return w, invalid("security is required")
	default:
		return w, invalid("unknown security %q", w.Security)
	}

	if slices.Contains(w.APGroups, "") {
		return w, invalid("ap_groups must not contain empty names")
	}
	return w, nil
}

func validWPAKey(k string) bool {
	if len(k) == 64 {
		return isHex(k)
	}
	return len(k) >= 8 && len(k) <= 63 && isPrintableASCII(k)
}

func validWEPKey(k string) bool {
	switch len(k) {
	case 5, 13:
		return isPrintableASCII(k)
	case 10, 26:
		return isHex(k)
	}
	return false
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}
