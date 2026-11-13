package unifisettings_test

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jacobalberty/beenfar/adapters/unifi"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
	"github.com/jacobalberty/beenfar/internal/unifisettings"
)

func open(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "beenfar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestEnsureGeneratesOnceAndKeepsIt(t *testing.T) {
	ctx := t.Context()
	st := open(t)
	if err := unifisettings.Ensure(ctx, st, unifi.SiteOptions{}); err != nil {
		t.Fatal(err)
	}
	first, err := unifisettings.Load(ctx, st)
	if err != nil || first.Country != 0 || first.AdminPassword == "" || first.ControllerKey == "" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	// A restart keeps every generated value.
	if err := unifisettings.Ensure(ctx, st, unifi.SiteOptions{}); err != nil {
		t.Fatal(err)
	}
	again, _ := unifisettings.Load(ctx, st)
	if again.AdminPassword != first.AdminPassword || again.ControllerKey != first.ControllerKey || again.SiteID != first.SiteID {
		t.Error("a restart regenerated values")
	}
	// The operator's settings are applied, and only those.
	if err := unifisettings.Ensure(ctx, st, unifi.SiteOptions{Country: 826, LocaleTimezone: "Europe/London", NTPServers: []string{"ntp.example.test"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := unifisettings.Load(ctx, st)
	if got.Country != 826 || got.LocaleTimezone != "Europe/London" || got.SystemTimezone != "UTC" || got.NTPServers[0] != "ntp.example.test" {
		t.Errorf("operator settings not applied: %+v", got)
	}
	if got.AdminPassword != first.AdminPassword {
		t.Error("applying operator settings regenerated a secret")
	}
}

// Nothing the package logs contains a secret or a generated value.
func TestEnsureNeverLogsSecrets(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	ctx := t.Context()
	st := open(t)
	for range 2 {
		if err := unifisettings.Ensure(ctx, st, unifi.SiteOptions{Country: 840}); err != nil {
			t.Fatal(err)
		}
	}
	if err := unifisettings.Ensure(ctx, st, unifi.SiteOptions{Country: 826}); err != nil {
		t.Fatal(err)
	}
	site, _ := unifisettings.Load(ctx, st)
	for name, v := range map[string]string{
		"admin password": site.AdminPassword, "admin hash": site.AdminPasswordHash, "controller key": site.ControllerKey,
		"mesh psk": site.MeshPSK, "site id": site.SiteID, "reporter id": site.ReporterID,
	} {
		if v == "" || strings.Contains(buf.String(), v) {
			t.Errorf("%s is empty or was logged", name)
		}
	}
	if buf.Len() == 0 {
		t.Error("nothing was logged at all")
	}
}

func TestSource(t *testing.T) {
	ctx := t.Context()
	st := open(t)
	src := unifisettings.Source{Store: st}
	if _, err := src.Site(ctx); err == nil {
		t.Error("site settings before Ensure")
	}
	d, err := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:20", Adapter: "unifi"})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := src.DeviceSettings(ctx, d.ID); err != nil || v != nil {
		t.Fatalf("nothing stored: %v, %v", v, err)
	}
	if err := st.SetDeviceSettings(ctx, d.ID, store.DeviceSettings{Original: map[string]string{"a": "1"}, Current: map[string]string{"a": "2"}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := src.DeviceSettings(ctx, d.ID); v["a"] != "2" {
		t.Errorf("current = %v", v)
	}
}
