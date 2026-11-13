package core

import (
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
)

func openCoreStore(t *testing.T) store.Store {
	t.Helper()
	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "beenfar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestNormalizeWifi(t *testing.T) {
	ok := store.WifiNetwork{SSID: "home", Security: store.WifiSecurityWPAPersonal, SecurityKey: "12345678"}
	w, err := normalizeWifi(ok)
	if err != nil || w.Band != store.WifiBandBoth {
		t.Fatalf("normalize = %+v, %v", w, err)
	}

	mod := func(f func(*store.WifiNetwork)) store.WifiNetwork { w := ok; f(&w); return w }
	for name, w := range map[string]store.WifiNetwork{
		"no ssid":           mod(func(w *store.WifiNetwork) { w.SSID = "" }),
		"long ssid":         mod(func(w *store.WifiNetwork) { w.SSID = "0123456789abcdef0123456789abcdefX" }),
		"no security":       mod(func(w *store.WifiNetwork) { w.Security = "" }),
		"short wpa key":     mod(func(w *store.WifiNetwork) { w.SecurityKey = "1234567" }),
		"64 non-hex":        mod(func(w *store.WifiNetwork) { w.SecurityKey = string(make([]byte, 64)) }),
		"open with key":     mod(func(w *store.WifiNetwork) { w.Security = store.WifiSecurityOpen }),
		"wep bad length":    mod(func(w *store.WifiNetwork) { w.Security = store.WifiSecurityWEP }),
		"enterprise radius": mod(func(w *store.WifiNetwork) { w.Security, w.SecurityKey = store.WifiSecurityWPAEnterprise, "" }),
		"empty ap group":    mod(func(w *store.WifiNetwork) { w.APGroups = []string{""} }),
		"bad band":          mod(func(w *store.WifiNetwork) { w.Band = "6g" }),
	} {
		if _, err := normalizeWifi(w); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	for _, key := range []string{"abcde", "0123456789", "0123456789abcdef0123456789"} {
		if _, err := normalizeWifi(mod(func(w *store.WifiNetwork) { w.Security, w.SecurityKey = store.WifiSecurityWEP, key })); err != nil {
			t.Errorf("WEP key %q rejected: %v", key, err)
		}
	}
}

func TestNormalizeNetwork(t *testing.T) {
	n, err := normalizeNetwork(store.Network{
		Name: "lan", Subnet: "192.168.010.1/24", DHCPEnabled: true, DHCPStart: "192.168.10.100", DHCPStop: "192.168.10.200",
	})
	if err == nil {
		t.Fatalf("leading zeros accepted: %+v", n)
	}
	n, err = normalizeNetwork(store.Network{
		Name: "lan", VLANID: 10, Subnet: "192.168.10.1/24", DHCPEnabled: true, DHCPStart: "192.168.10.100", DHCPStop: "192.168.10.200",
	})
	if err != nil || n.Subnet != "192.168.10.1/24" {
		t.Fatalf("normalize = %+v, %v", n, err)
	}
	for name, bad := range map[string]store.Network{
		"no name":         {},
		"vlan":            {Name: "x", VLANID: 4095},
		"ipv6":            {Name: "x", Subnet: "fd00::1/64"},
		"network addr":    {Name: "x", Subnet: "10.0.0.0/24"},
		"broadcast addr":  {Name: "x", Subnet: "10.0.0.255/24"},
		"/31":             {Name: "x", Subnet: "10.0.0.1/31"},
		"dhcp no subnet":  {Name: "x", DHCPEnabled: true},
		"dhcp no range":   {Name: "x", Subnet: "10.0.0.1/24", DHCPEnabled: true},
		"dhcp outside":    {Name: "x", Subnet: "10.0.0.1/24", DHCPStart: "10.0.1.1"},
		"dhcp reversed":   {Name: "x", Subnet: "10.0.0.1/24", DHCPStart: "10.0.0.20", DHCPStop: "10.0.0.10"},
		"bad domain name": {Name: "x", DomainName: "has space"},
	} {
		if _, err := normalizeNetwork(bad); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestLastAddr(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.1/24": "10.0.0.255", "10.0.0.1/8": "10.255.255.255", "10.0.0.1/30": "10.0.0.3", "10.1.2.3/20": "10.1.15.255",
	} {
		if got := lastAddr(netip.MustParsePrefix(in)).String(); got != want {
			t.Errorf("lastAddr(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestDeviceUpdateAPGroup(t *testing.T) {
	ctx := t.Context()
	st := openCoreStore(t)
	svc := New(st, nil)
	ap, _ := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:02:01", Adapter: "x", Kind: store.DeviceKindAccessPoint})
	sw, _ := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:02:02", Adapter: "x", Kind: store.DeviceKindSwitch, PortCount: 8})

	if d, err := svc.Devices.Update(ctx, store.Device{ID: ap.ID, APGroup: "attic"}, []string{"ap_group"}); err != nil || d.APGroup != "attic" {
		t.Fatalf("update = %+v, %v", d, err)
	}
	// Wifi networks accept any group name, so a device can be put in any of them.
	longName := strings.Repeat("g", 100)
	if d, err := svc.Devices.Update(ctx, store.Device{ID: ap.ID, APGroup: longName}, []string{"ap_group"}); err != nil || d.APGroup != longName {
		t.Fatalf("long group name = %+v, %v", d, err)
	}
	if _, err := svc.Devices.Update(ctx, store.Device{ID: sw.ID, APGroup: "attic"}, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("ap_group on a switch = %v, want ErrInvalid", err)
	}
	if _, err := svc.Devices.Update(ctx, store.Device{ID: ap.ID}, []string{"model"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown path = %v, want ErrInvalid", err)
	}
	if _, err := svc.Devices.Update(ctx, store.Device{ID: "nope"}, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown device = %v, want ErrNotFound", err)
	}
}

func TestSwitchPortsOnlyOnDevicesWithPorts(t *testing.T) {
	ctx := t.Context()
	st := openCoreStore(t)
	svc := New(st, NoAdapters{})
	adopt := func(mac string, d store.DiscoveredDevice) store.Device {
		d.MAC, d.Adapter = mac, "x"
		dev, err := st.ReportDevice(ctx, d)
		if err == nil {
			dev, err = st.AdoptDevice(ctx, dev.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		return dev
	}
	ap := adopt("de:ad:be:ef:03:01", store.DiscoveredDevice{Kind: store.DeviceKindAccessPoint})
	inwall := adopt("de:ad:be:ef:03:02", store.DiscoveredDevice{Kind: store.DeviceKindAccessPoint, PortCount: 4})
	sw := adopt("de:ad:be:ef:03:03", store.DiscoveredDevice{Kind: store.DeviceKindSwitch, PortCount: 8})
	other := adopt("de:ad:be:ef:03:04", store.DiscoveredDevice{}) // kind not reported

	if _, err := svc.Ports.Create(ctx, store.SwitchPort{DeviceID: ap.ID, PortIndex: 1}); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Errorf("port on an access point without ports = %v, want ErrFailedPrecondition", err)
	}
	if _, err := svc.Ports.Create(ctx, store.SwitchPort{DeviceID: inwall.ID, PortIndex: 4}); err != nil {
		t.Errorf("port on an in-wall access point: %v", err)
	}
	if _, err := svc.Ports.Create(ctx, store.SwitchPort{DeviceID: sw.ID, PortIndex: 9}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("port 9 of 8 = %v, want ErrInvalid", err)
	}
	if _, err := svc.Ports.Create(ctx, store.SwitchPort{DeviceID: other.ID, PortIndex: 30}); err != nil {
		t.Errorf("port on a device of unknown kind: %v", err)
	}
}
