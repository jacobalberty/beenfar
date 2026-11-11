package server

import (
	"fmt"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1"
	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
)

// Etags are the decimal store revision. Clients must treat them as opaque.
func etag(revision int64) string { return strconv.FormatInt(revision, 10) }

// parseEtag maps an etag to a revision. Empty means "no check" (0).
func parseEtag(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: malformed etag %q", store.ErrInvalid, s)
	}
	return n, nil
}

func ts(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// --- wifi networks ---

var wifiSecurityToStore = map[configv1.WifiSecurity]store.WifiSecurity{
	configv1.WifiSecurity_WIFI_SECURITY_OPEN:           store.WifiSecurityOpen,
	configv1.WifiSecurity_WIFI_SECURITY_WEP:            store.WifiSecurityWEP,
	configv1.WifiSecurity_WIFI_SECURITY_WPA_PERSONAL:   store.WifiSecurityWPAPersonal,
	configv1.WifiSecurity_WIFI_SECURITY_WPA_ENTERPRISE: store.WifiSecurityWPAEnterprise,
}

var wifiBandToStore = map[configv1.WifiBand]store.WifiBand{
	configv1.WifiBand_WIFI_BAND_BOTH: store.WifiBandBoth,
	configv1.WifiBand_WIFI_BAND_2G:   store.WifiBand2G,
	configv1.WifiBand_WIFI_BAND_5G:   store.WifiBand5G,
}

func invert[K, V comparable](m map[K]V) map[V]K {
	out := make(map[V]K, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}

var (
	wifiSecurityFromStore = invert(wifiSecurityToStore)
	wifiBandFromStore     = invert(wifiBandToStore)
	deviceStateFromStore  = invert(deviceStateToStore)
)

func wifiToStore(w *configv1.WifiNetwork) (store.WifiNetwork, error) {
	if w == nil {
		return store.WifiNetwork{}, fmt.Errorf("%w: wifi_network is required", store.ErrInvalid)
	}
	rev, err := parseEtag(w.GetEtag())
	if err != nil {
		return store.WifiNetwork{}, err
	}
	sec, ok := wifiSecurityToStore[w.GetSecurity()]
	if !ok && w.GetSecurity() != configv1.WifiSecurity_WIFI_SECURITY_UNSPECIFIED {
		return store.WifiNetwork{}, fmt.Errorf("%w: unknown security %v", store.ErrInvalid, w.GetSecurity())
	}
	band, ok := wifiBandToStore[w.GetBand()]
	if !ok && w.GetBand() != configv1.WifiBand_WIFI_BAND_UNSPECIFIED {
		return store.WifiNetwork{}, fmt.Errorf("%w: unknown band %v", store.ErrInvalid, w.GetBand())
	}
	return store.WifiNetwork{
		ID:               w.GetId(),
		SSID:             w.GetSsid(),
		Security:         sec,
		SecurityKey:      w.GetSecurityKey(),
		Band:             band,
		NetworkID:        w.GetNetworkId(),
		Guest:            w.GetGuest(),
		Hidden:           w.GetHidden(),
		APGroups:         w.GetApGroups(),
		DefaultUserGroup: w.GetDefaultUserGroup(),
		RadiusProfile:    w.GetRadiusProfile(),
		Revision:         rev,
	}, nil
}

func wifiFromStore(w store.WifiNetwork) *configv1.WifiNetwork {
	return &configv1.WifiNetwork{
		Id:               w.ID,
		Ssid:             w.SSID,
		Security:         wifiSecurityFromStore[w.Security],
		SecurityKey:      w.SecurityKey,
		Band:             wifiBandFromStore[w.Band],
		NetworkId:        w.NetworkID,
		Guest:            w.Guest,
		Hidden:           w.Hidden,
		ApGroups:         w.APGroups,
		DefaultUserGroup: w.DefaultUserGroup,
		RadiusProfile:    w.RadiusProfile,
		Etag:             etag(w.Revision),
		CreateTime:       ts(w.CreateTime),
		UpdateTime:       ts(w.UpdateTime),
	}
}

// --- networks ---

func networkToStore(n *configv1.Network) (store.Network, error) {
	if n == nil {
		return store.Network{}, fmt.Errorf("%w: network is required", store.ErrInvalid)
	}
	rev, err := parseEtag(n.GetEtag())
	if err != nil {
		return store.Network{}, err
	}
	if n.GetVlanId() > 4094 {
		return store.Network{}, fmt.Errorf("%w: vlan_id must be 0 (untagged) or 1-4094", store.ErrInvalid)
	}
	return store.Network{
		ID:          n.GetId(),
		Name:        n.GetName(),
		VLANID:      int(n.GetVlanId()),
		Subnet:      n.GetSubnet(),
		DHCPEnabled: n.GetDhcpEnabled(),
		DHCPStart:   n.GetDhcpStart(),
		DHCPStop:    n.GetDhcpStop(),
		DomainName:  n.GetDomainName(),
		Revision:    rev,
	}, nil
}

func networkFromStore(n store.Network) *configv1.Network {
	return &configv1.Network{
		Id:          n.ID,
		Name:        n.Name,
		VlanId:      uint32(n.VLANID), //nolint:gosec // 0-4094, enforced on write
		Subnet:      n.Subnet,
		DhcpEnabled: n.DHCPEnabled,
		DhcpStart:   n.DHCPStart,
		DhcpStop:    n.DHCPStop,
		DomainName:  n.DomainName,
		Etag:        etag(n.Revision),
		CreateTime:  ts(n.CreateTime),
		UpdateTime:  ts(n.UpdateTime),
	}
}

// --- devices ---

var deviceStateToStore = map[configv1.DeviceState]store.DeviceState{
	configv1.DeviceState_DEVICE_STATE_PENDING: store.DeviceStatePending,
	configv1.DeviceState_DEVICE_STATE_ADOPTED: store.DeviceStateAdopted,
}

func deviceFromStore(d store.Device) *configv1.Device {
	return &configv1.Device{
		Id:           d.ID,
		Mac:          d.MAC,
		Adapter:      d.Adapter,
		State:        deviceStateFromStore[d.State],
		Model:        d.Model,
		Ip:           d.IP,
		Firmware:     d.Firmware,
		LastSeenTime: ts(d.LastSeen),
		CreateTime:   ts(d.CreateTime),
		UpdateTime:   ts(d.UpdateTime),
	}
}

func devicesFromStore(ds []store.Device) []*configv1.Device {
	out := make([]*configv1.Device, 0, len(ds))
	for _, d := range ds {
		out = append(out, deviceFromStore(d))
	}
	return out
}

// --- admin ---

func userFromStore(u store.User) *adminv1.User {
	return &adminv1.User{
		Id:         u.ID,
		Username:   u.Username,
		Admin:      u.Admin,
		Etag:       etag(u.Revision),
		CreateTime: ts(u.CreateTime),
		UpdateTime: ts(u.UpdateTime),
	}
}

func apiTokenFromStore(t store.APIToken) *adminv1.ApiToken {
	return &adminv1.ApiToken{
		Id:           t.ID,
		Name:         t.Name,
		Prefix:       "bf_" + t.Prefix,
		UserId:       t.UserID,
		ExpireTime:   ts(t.ExpireTime),
		LastUsedTime: ts(t.LastUsedTime),
		CreateTime:   ts(t.CreateTime),
	}
}

var adapterKindFromCore = map[core.AdapterKind]adminv1.AdapterKind{
	core.AdapterBuiltin:  adminv1.AdapterKind_ADAPTER_KIND_BUILTIN,
	core.AdapterExternal: adminv1.AdapterKind_ADAPTER_KIND_EXTERNAL,
}

func adapterFromCore(a core.AdapterStatus) *adminv1.Adapter {
	return &adminv1.Adapter{
		Name:          a.Name,
		Kind:          adapterKindFromCore[a.Kind],
		Version:       a.Version,
		Healthy:       a.Healthy,
		StatusMessage: a.StatusMessage,
		ConnectTime:   ts(a.ConnectTime),
	}
}
