package core

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/jacobalberty/beenfar/internal/store"
)

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
