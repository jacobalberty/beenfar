package unifi

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/jacobalberty/beenfar/api/adapter"
)

func testSite(t *testing.T) SiteSettings {
	t.Helper()
	s, err := NewSite(SiteOptions{Country: 840})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func desired() adapter.DesiredConfig {
	return adapter.DesiredConfig{
		Networks: []adapter.Network{{ID: "net-iot", VLANID: 20}},
		Wifi: []adapter.WifiNetwork{
			{ID: "w2", SSID: "corp", Security: adapter.WifiWPAEnterprise, RadiusServerID: "r1"},
			{ID: "w1", SSID: "home", Security: adapter.WifiWPAPersonal, SecurityKey: "hunter2hunter2", Band: adapter.Band5G},
			{ID: "w3", SSID: "iot", Security: adapter.WifiWPAPersonal, SecurityKey: "iotiotiotiot", NetworkID: "net-iot", Hidden: true},
		},
		RadiusServers: []adapter.RadiusServer{{ID: "r1", Address: "192.0.2.10", AuthPort: 1812, AccountingPort: 1813, SharedSecret: "radius-secret"}},
	}
}

func TestRenderSystemConfigMapsTheConfiguration(t *testing.T) {
	site := testSite(t)
	doc, err := renderSystemConfig("U7LT", desired(), nil, site, "ctl.example.test:8080")
	if err != nil {
		t.Fatal(err)
	}
	// Networks render in SSID order: corp (both bands) is aaa.1/2, home (5 GHz) aaa.3, iot aaa.4/5.
	for k, want := range map[string]string{
		"aaa.1.ssid": "corp", "aaa.1.wpa.key.1.mgmt": "WPA-EAP", "aaa.1.radius.auth.1.ip": "192.0.2.10",
		"aaa.1.radius.acct.1.port": "1813", "aaa.2.ssid": "corp",
		"aaa.3.ssid": "home", "aaa.3.wpa.psk": "hunter2hunter2", "wireless.3.parent": "wifi1",
		"aaa.4.ssid": "iot", "aaa.4.hide_ssid": "true", "aaa.4.br.devname": "br0.20", "vlan.1.id": "20",
		"unifi.mcip": "http://ctl.example.test:8080/inform", "radio.countrycode": "840",
		"users.1.password": site.AdminPasswordHash,
	} {
		if got := doc[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	// The per-network secret is derived, 32 hex digits, per network and per site.
	hex32 := regexp.MustCompile(`^[0-9a-f]{32}$`)
	k1, k2 := doc["aaa.1.iapp_key"], doc["aaa.3.iapp_key"]
	if !hex32.MatchString(k1) || !hex32.MatchString(k2) || k1 == k2 || k1 != doc["aaa.2.iapp_key"] {
		t.Errorf("iapp keys %q, %q, %q", k1, k2, doc["aaa.2.iapp_key"])
	}
	other := site
	other.ControllerKey = strings.Repeat("0", 32)
	doc2, err := renderSystemConfig("U7LT", desired(), nil, other, "ctl.example.test:8080")
	if err != nil {
		t.Fatal(err)
	}
	if doc2["aaa.1.iapp_key"] == k1 {
		t.Error("the iapp key does not depend on the site")
	}
}

func TestRenderSystemConfigIsDeterministicAndRevisioned(t *testing.T) {
	site := testSite(t)
	a, _ := renderSystemConfig("U7LT", desired(), nil, site, "h:1")
	b, _ := renderSystemConfig("U7LT", desired(), nil, site, "h:1")
	if a.String() != b.String() || documentRevision(a) != documentRevision(b) {
		t.Fatal("same inputs, different documents")
	}
	if documentRevision(a) <= 0 {
		t.Fatalf("revision %d", documentRevision(a))
	}
	base := documentRevision(a)
	changed := func(name string, doc func() (map[string]string, error)) {
		t.Helper()
		d, err := doc()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if documentRevision(d) == base {
			t.Errorf("%s did not change the revision", name)
		}
	}
	changed("a tunable", func() (map[string]string, error) {
		return renderSystemConfig("U7LT", desired(), map[string]string{"radio.2.channel": "36"}, site, "h:1")
	})
	changed("the site", func() (map[string]string, error) {
		s := site
		s.SSHEnabled = false
		return renderSystemConfig("U7LT", desired(), nil, s, "h:1")
	})
	changed("a network", func() (map[string]string, error) {
		d := desired()
		d.Wifi[1].SecurityKey = "anotherpassphrase"
		return renderSystemConfig("U7LT", d, nil, site, "h:1")
	})
}

func TestRenderSystemConfigRefusals(t *testing.T) {
	site := testSite(t)
	noCountry := site
	noCountry.Country = 0
	for name, c := range map[string]struct {
		model   string
		mutate  func(*adapter.DesiredConfig)
		site    SiteSettings
		host    string
		noDoc   bool // an errNoDocument, rather than an error of the configuration
		tunable map[string]string
	}{
		"no profile":        {model: "U7PG2", site: site, host: "h", noDoc: true},
		"no networks":       {model: "U7LT", mutate: func(d *adapter.DesiredConfig) { d.Wifi = nil }, site: site, host: "h", noDoc: true},
		"open":              {model: "U7LT", mutate: func(d *adapter.DesiredConfig) { d.Wifi[0].Security = adapter.WifiOpen }, site: site, host: "h"},
		"wep":               {model: "U7LT", mutate: func(d *adapter.DesiredConfig) { d.Wifi[0].Security = adapter.WifiWEP }, site: site, host: "h"},
		"guest":             {model: "U7LT", mutate: func(d *adapter.DesiredConfig) { d.Wifi[0].Guest = true }, site: site, host: "h"},
		"unknown radius":    {model: "U7LT", mutate: func(d *adapter.DesiredConfig) { d.Wifi[0].RadiusServerID = "nope" }, site: site, host: "h"},
		"unknown network":   {model: "U7LT", mutate: func(d *adapter.DesiredConfig) { d.Wifi[2].NetworkID = "nope" }, site: site, host: "h"},
		"short passphrase":  {model: "U7LT", mutate: func(d *adapter.DesiredConfig) { d.Wifi[1].SecurityKey = "short" }, site: site, host: "h"},
		"no country":        {model: "U7LT", site: noCountry, host: "h"},
		"no inform address": {model: "U7LT", site: site, host: ""},
		"bad tunable":       {model: "U7LT", site: site, host: "h", tunable: map[string]string{"radio.1.channel": "x"}},
		"unknown tunable":   {model: "U7LT", site: site, host: "h", tunable: map[string]string{"radio.1.cwm.mode": "1"}},
	} {
		d := desired()
		if c.mutate != nil {
			c.mutate(&d)
		}
		doc, err := renderSystemConfig(c.model, d, c.tunable, c.site, c.host)
		if err == nil || doc != nil {
			t.Errorf("%s: rendered (%v)", name, err)
			continue
		}
		if errors.Is(err, errNoDocument) != c.noDoc {
			t.Errorf("%s: errNoDocument = %v, want %v (%v)", name, errors.Is(err, errNoDocument), c.noDoc, err)
		}
	}
}

func TestSettingsSchema(t *testing.T) {
	a := &Adapter{}
	defaults, check, ok := a.SettingsSchema("u7lt")
	if !ok || defaults["radio.1.channel"] != "auto" || check == nil {
		t.Fatalf("schema = %v, %v", defaults, ok)
	}
	if err := check(map[string]string{"radio.1.channel": "11"}); err != nil {
		t.Error(err)
	}
	if err := check(map[string]string{"radio.1.channel": "x"}); err == nil {
		t.Error("an invalid channel was accepted")
	}
	if err := check(map[string]string{"no.such.key": "1"}); err == nil {
		t.Error("an unknown key was accepted")
	}
	if _, _, ok := a.SettingsSchema("U7PG2"); ok {
		t.Error("a model without a profile has settings")
	}
}

func TestNewSiteGeneratesUniqueSecrets(t *testing.T) {
	a, err := NewSite(SiteOptions{Country: 840})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewSite(SiteOptions{Country: 840})
	for name, pair := range map[string][2]string{
		"controller key": {a.ControllerKey, b.ControllerKey}, "site id": {a.SiteID, b.SiteID},
		"reporter id": {a.ReporterID, b.ReporterID}, "anonymous site id": {a.AnonymousSiteID, b.AnonymousSiteID},
		"anonymous controller id": {a.AnonymousControllerID, b.AnonymousControllerID},
		"mesh psk":                {a.MeshPSK, b.MeshPSK}, "mesh essid": {a.MeshESSID, b.MeshESSID},
		"admin password": {a.AdminPassword, b.AdminPassword}, "admin hash": {a.AdminPasswordHash, b.AdminPasswordHash},
	} {
		if pair[0] == "" || pair[0] == pair[1] {
			t.Errorf("%s: %q and %q", name, pair[0], pair[1])
		}
	}
	if !strings.HasPrefix(a.AdminPasswordHash, "$6$") {
		t.Errorf("admin hash %q is not a SHA-512 crypt hash", a.AdminPasswordHash)
	}
	if got, _ := sha512Crypt(a.AdminPassword, strings.Split(a.AdminPasswordHash, "$")[2]); got != a.AdminPasswordHash {
		t.Error("the admin hash does not match the admin password")
	}
	// No country unless asked for: it is a regulatory setting.
	if z, _ := NewSite(SiteOptions{}); z.Country != 0 {
		t.Errorf("default country %d", z.Country)
	}
	// It survives storage as JSON.
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var back SiteSettings
	if err := json.Unmarshal(raw, &back); err != nil || back.AdminPasswordHash != a.AdminPasswordHash || back.MeshPSK != a.MeshPSK {
		t.Fatalf("round trip: %v", err)
	}
}
