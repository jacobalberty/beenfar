package unifi

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/jacobalberty/beenfar/api/adapter"
)

// capturedKeys reads the key names of the real device's system.cfg.
func capturedKeys(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open("testdata/system-cfg-keys.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	keys := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" && !strings.HasPrefix(line, "#") {
			keys[line] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return keys
}

// normalizeKey replaces numeric components with N: aaa.12.radius.auth.1.ip
// becomes aaa.N.radius.auth.N.ip.
func normalizeKey(k string) string {
	parts := strings.Split(k, ".")
	for i, p := range parts {
		if p != "" && strings.Trim(p, "0123456789") == "" {
			parts[i] = "N"
		}
	}
	return strings.Join(parts, ".")
}

func corpConfig() adapter.DesiredConfig {
	return adapter.DesiredConfig{
		Networks: []adapter.Network{{ID: "n-iot", Name: "iot", VLANID: 5}},
		Wifi: []adapter.WifiNetwork{
			{ID: "w-corp", SSID: "corp", Security: adapter.WifiWPAEnterprise, Band: adapter.BandBoth, RadiusServerID: "r1"},
			{ID: "w-iot", SSID: "iot", Security: adapter.WifiWPAPersonal, SecurityKey: "hunter2hunter2", Band: adapter.Band2G, NetworkID: "n-iot", Hidden: true},
		},
		RadiusServers: []adapter.RadiusServer{{ID: "r1", Address: "10.2.1.76", AuthPort: 1812, AccountingPort: 1813, SharedSecret: "s3cret"}},
		SwitchPorts:   []adapter.SwitchPort{{PortIndex: 1, Tagging: adapter.SwitchPortTagging("all"), PoEMode: adapter.PoEMode("off")}},
	}
}

// Every key the renderer writes was seen on a real device.
func TestRenderedKeysWereCaptured(t *testing.T) {
	known := capturedKeys(t)
	cfg, err := renderSystemConfig(corpConfig())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Len() == 0 {
		t.Fatal("nothing rendered")
	}
	for line := range strings.SplitSeq(strings.TrimSpace(cfg.String()), "\n") {
		k, _, _ := strings.Cut(line, "=")
		if !known[normalizeKey(k)] {
			t.Errorf("rendered key %q (%s) does not appear in the captured system.cfg", k, normalizeKey(k))
		}
	}
}

func TestRenderSystemConfig(t *testing.T) {
	cfg, err := renderSystemConfig(corpConfig())
	if err != nil {
		t.Fatal(err)
	}
	// corp is on both radios (wifi0ap0, wifi1ap1) on br0; iot is on the
	// 2.4 GHz radio only (wifi0ap2), on VLAN 5.
	for k, want := range map[string]string{
		"aaa.1.devname":              "wifi0ap0",
		"aaa.1.ssid":                 "corp",
		"aaa.1.br.devname":           "br0",
		"aaa.1.id":                   "w-corp",
		"aaa.1.wpa.key.1.mgmt":       "WPA-EAP",
		"aaa.1.radius.auth.1.ip":     "10.2.1.76",
		"aaa.1.radius.auth.1.port":   "1812",
		"aaa.1.radius.auth.1.secret": "s3cret",
		"aaa.1.radius.acct.1.port":   "1813",
		"aaa.1.dynamic_vlan":         "0",
		"aaa.2.devname":              "wifi1ap1",
		"wireless.2.parent":          "wifi1",
		"aaa.3.devname":              "wifi0ap2",
		"aaa.3.br.devname":           "br0.5",
		"aaa.3.wpa.key.1.mgmt":       "WPA-PSK",
		"aaa.3.wpa.psk":              "hunter2hunter2",
		"aaa.3.hide_ssid":            "true",
		"wireless.3.hide_ssid":       "true",
		"radio.1.devname":            "wifi0ap0",
		"radio.1.virtual.1.devname":  "wifi0ap2",
		"radio.2.devname":            "wifi1ap1",
		"bridge.1.devname":           "br0",
		"bridge.1.port.1.devname":    "eth0",
		"bridge.1.port.2.devname":    "wifi0ap0",
		"bridge.1.port.3.devname":    "wifi1ap1",
		"bridge.2.devname":           "br0.5",
		"bridge.2.port.1.devname":    "eth0.5",
		"bridge.2.port.2.devname":    "wifi0ap2",
		"vlan.1.devname":             "eth0",
		"vlan.1.id":                  "5",
		"vlan.status":                "enabled",
	} {
		if got, ok := cfg.Get(k); !ok || got != want {
			t.Errorf("%s = %q (set %v), want %q", k, got, ok, want)
		}
	}
	for _, absent := range []string{"aaa.1.wpa.psk", "aaa.3.radius.auth.1.ip", "aaa.4.ssid", "radio.2.virtual.1.devname"} {
		if v, ok := cfg.Get(absent); ok {
			t.Errorf("%s = %q, want it unset", absent, v)
		}
	}

	// Without accounting, no accounting server is configured.
	noAcct := corpConfig()
	noAcct.RadiusServers[0].AccountingPort = 0
	cfg, err = renderSystemConfig(noAcct)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := cfg.Get("aaa.1.radius.acct.1.ip"); ok {
		t.Errorf("accounting rendered without an accounting port: %q", v)
	}

	// Rendering is deterministic.
	a, _ := renderSystemConfig(corpConfig())
	b, _ := renderSystemConfig(corpConfig())
	if a.String() != b.String() {
		t.Error("two renders of the same config differ")
	}
}

func TestRenderSystemConfigEmpty(t *testing.T) {
	cfg, err := renderSystemConfig(adapter.DesiredConfig{Networks: []adapter.Network{{ID: "n", VLANID: 7}}})
	if err != nil || cfg.Len() != 0 {
		t.Fatalf("no wifi: %q, %v", cfg.String(), err)
	}
}

// What the capture does not show is refused rather than guessed.
func TestRenderSystemConfigUnsupported(t *testing.T) {
	for name, w := range map[string]adapter.WifiNetwork{
		"open":           {SSID: "o", Security: adapter.WifiOpen},
		"wep":            {SSID: "w", Security: adapter.WifiWEP, SecurityKey: "12345"},
		"guest":          {SSID: "g", Security: adapter.WifiWPAPersonal, SecurityKey: "hunter2hunter2", Guest: true},
		"no radius":      {SSID: "e", Security: adapter.WifiWPAEnterprise},
		"unknown radius": {SSID: "e", Security: adapter.WifiWPAEnterprise, RadiusServerID: "missing"},
		"unknown net":    {SSID: "n", Security: adapter.WifiWPAPersonal, SecurityKey: "hunter2hunter2", NetworkID: "missing"},
	} {
		if _, err := renderSystemConfig(adapter.DesiredConfig{Wifi: []adapter.WifiNetwork{w}}); err == nil {
			t.Errorf("%s: rendered", name)
		}
	}
}
