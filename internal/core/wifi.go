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

// Update replaces every mutable field. A non-zero w.Revision must match.
func (s *WifiService) Update(ctx context.Context, w store.WifiNetwork) (store.WifiNetwork, error) {
	w, err := normalizeWifi(w)
	if err != nil {
		return store.WifiNetwork{}, err
	}
	return s.store.UpdateWifiNetwork(ctx, w)
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
