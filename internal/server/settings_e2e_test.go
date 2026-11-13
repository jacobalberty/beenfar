package server_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/jacobalberty/go-unifi/inform"

	"github.com/jacobalberty/beenfar/adapters/unifi"
	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/unifisettings"
)

// lateSource is a SettingsSource whose store exists only once the fixture is built.
type lateSource struct {
	mu  sync.Mutex
	src unifi.SettingsSource
}

func (l *lateSource) set(s unifi.SettingsSource) { l.mu.Lock(); l.src = s; l.mu.Unlock() }

func (l *lateSource) get() unifi.SettingsSource { l.mu.Lock(); defer l.mu.Unlock(); return l.src }

func (l *lateSource) Site(ctx context.Context) (unifi.SiteSettings, error) { return l.get().Site(ctx) }

func (l *lateSource) DeviceSettings(ctx context.Context, id string) (map[string]string, error) {
	return l.get().DeviceSettings(ctx, id)
}

func systemCfgOf(t *testing.T, reply map[string]any) *inform.Config {
	t.Helper()
	sys, ok := reply["system_cfg"].(string)
	if !ok {
		t.Fatalf("no system_cfg in %v", reply)
	}
	cfg, err := inform.ParseConfig(sys)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func cfgVersionOf(t *testing.T, reply map[string]any) string {
	t.Helper()
	m, err := inform.ParseConfig(reply["mgmt_cfg"].(string))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := m.Get(inform.KeyCfgVersion)
	return v
}

// The settings API on a real UniFi adapter: the baseline is stored at adoption,
// a change reaches the access point's system_cfg, a reset undoes it, a bad value
// is refused, and a model without a profile has no settings.
func TestUnifiDeviceSettingsThroughTheAPI(t *testing.T) {
	src := &lateSource{}
	f, _ := newUnifiFixture(t, func(o *unifi.Options) { o.Settings = src })
	ctx := t.Context()
	if err := unifisettings.Ensure(ctx, f.store, unifi.SiteOptions{Country: 840}); err != nil {
		t.Fatal(err)
	}
	src.set(unifisettings.Source{Store: f.store})
	if _, err := f.store.CreateWifiNetwork(ctx, store.WifiNetwork{
		SSID: "home", Security: store.WifiSecurityWPAPersonal, SecurityKey: "hunter2hunter2", Band: store.WifiBandBoth,
	}); err != nil {
		t.Fatal(err)
	}
	devices := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))
	ap := unifiDevice{t: t, f: f, mac: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x20, 0x01}, model: "U7LT"}

	ap.inform(inform.DefaultKey(), "")
	pending, err := devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	if err != nil || len(pending.GetDevices()) != 1 {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	id := pending.GetDevices()[0].GetId()
	if _, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: id}}); err != nil {
		t.Fatal(err)
	}

	get := func() *configv1.DeviceSettings {
		t.Helper()
		r, err := devices.GetDeviceSettings(ctx, &configv1.GetDeviceSettingsRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetSettings()
	}
	s := get()
	if s.GetOriginal()["radio.1.channel"] != "auto" || s.GetCurrent()["radio.2.ieee_mode"] == "" || len(s.GetOriginal()) != len(s.GetCurrent()) {
		t.Fatalf("settings after adoption: %v", s)
	}

	_, reply := ap.inform(inform.DefaultKey(), "")
	key, version := authKeyOf(t, reply)
	_, reply = ap.inform(key, version)
	if got, _ := systemCfgOf(t, reply).Get("radio.1.channel"); got != "auto" {
		t.Fatalf("radio.1.channel = %q in the first document", got)
	}
	version = cfgVersionOf(t, reply)
	if _, reply := ap.inform(key, version); reply["_type"] != "noop" {
		t.Fatalf("after the document: %v", reply)
	}

	// Setting a value changes the current settings, and the access point's
	// next inform brings the new document.
	up, err := devices.UpdateDeviceSettings(ctx, &configv1.UpdateDeviceSettingsRequest{Id: id, Current: map[string]string{"radio.1.channel": "6"}})
	if err != nil {
		t.Fatal(err)
	}
	if up.GetSettings().GetCurrent()["radio.1.channel"] != "6" || up.GetSettings().GetOriginal()["radio.1.channel"] != "auto" {
		t.Fatalf("after update: %v", up.GetSettings())
	}
	_, reply = ap.inform(key, version)
	if got, _ := systemCfgOf(t, reply).Get("radio.1.channel"); got != "6" {
		t.Fatalf("radio.1.channel = %q after the update, want 6", got)
	}
	version = cfgVersionOf(t, reply)

	// Refused: an unknown setting, a bad value; nothing changes.
	for name, set := range map[string]map[string]string{
		"unknown key": {"radio.1.cwm.mode": "1"}, "bad value": {"radio.1.channel": "x"}, "country": {"radio.countrycode": "1"},
	} {
		_, err := devices.UpdateDeviceSettings(ctx, &configv1.UpdateDeviceSettingsRequest{Id: id, Current: set})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
	if get().GetCurrent()["radio.1.channel"] != "6" {
		t.Error("a refused update changed the settings")
	}

	// Over REST too.
	code, body, _ := f.rest(t, http.MethodGet, "/api/v1/devices/"+id+"/settings", f.adminToken, "")
	var rest struct {
		Original, Current map[string]string
	}
	if code != http.StatusOK || json.Unmarshal(body, &rest) != nil || rest.Current["radio.1.channel"] != "6" || rest.Original["radio.1.channel"] != "auto" {
		t.Fatalf("REST get: %d %s", code, body)
	}
	code, body, _ = f.rest(t, http.MethodPatch, "/api/v1/devices/"+id+"/settings", f.adminToken, `{"current":{"radio.2.channel":"36"}}`)
	if code != http.StatusOK {
		t.Fatalf("REST patch: %d %s", code, body)
	}
	code, body, _ = f.rest(t, http.MethodPost, "/api/v1/devices/"+id+"/settings:reset", f.adminToken, `{}`)
	if code != http.StatusOK || json.Unmarshal(body, &rest) != nil || rest.Current["radio.1.channel"] != "auto" || rest.Current["radio.2.channel"] != "auto" {
		t.Fatalf("REST reset: %d %s", code, body)
	}
	_, reply = ap.inform(key, version)
	if got, _ := systemCfgOf(t, reply).Get("radio.1.channel"); got != "auto" {
		t.Fatalf("radio.1.channel = %q after the reset, want auto", got)
	}

	// A model with no profile is adopted, has no settings, and cannot be given any.
	sw := unifiDevice{t: t, f: f, mac: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x20, 0x02}, model: "U7PG2"}
	sw.inform(inform.DefaultKey(), "")
	other, err := f.store.GetDeviceByMAC(ctx, sw.mac.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: other.ID}}); err != nil {
		t.Fatal(err)
	}
	r, err := devices.GetDeviceSettings(ctx, &configv1.GetDeviceSettingsRequest{Id: other.ID})
	if err != nil || len(r.GetSettings().GetOriginal())+len(r.GetSettings().GetCurrent()) != 0 {
		t.Fatalf("settings of a model without a profile: %v, %v", r, err)
	}
	_, err = devices.UpdateDeviceSettings(ctx, &configv1.UpdateDeviceSettingsRequest{Id: other.ID, Current: map[string]string{"radio.1.channel": "6"}})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("update on a model without a profile: %v", err)
	}
	if _, err := devices.GetDeviceSettings(ctx, &configv1.GetDeviceSettingsRequest{Id: "nope"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("unknown device: %v", err)
	}
}
