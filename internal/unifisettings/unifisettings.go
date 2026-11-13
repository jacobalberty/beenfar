// Package unifisettings keeps the UniFi adapter's settings in the store: the
// site settings, generated once at first start, and each device's tunable
// settings.
//
// The site settings hold secrets (the device admin password, the controller and
// mesh keys). They are stored in the adapter_settings table, are never
// returned by the API, and nothing here logs a value of them.
package unifisettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/jacobalberty/beenfar/adapters/unifi"
	"github.com/jacobalberty/beenfar/internal/store"
)

// Source implements unifi.SettingsSource on a store.
type Source struct {
	Store store.Store
}

var _ unifi.SettingsSource = Source{}

// Site returns the stored site settings.
func (s Source) Site(ctx context.Context) (unifi.SiteSettings, error) {
	return Load(ctx, s.Store)
}

// DeviceSettings returns the device's current tunable settings, or none when
// nothing was stored for it.
func (s Source) DeviceSettings(ctx context.Context, deviceID string) (map[string]string, error) {
	v, err := s.Store.GetDeviceSettings(ctx, deviceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return v.Current, err
}

// Load reads the site settings. It fails with store.ErrNotFound before Ensure
// has run.
func Load(ctx context.Context, st store.Store) (unifi.SiteSettings, error) {
	doc, err := st.GetAdapterSettings(ctx, unifi.Name)
	if err != nil {
		return unifi.SiteSettings{}, err
	}
	var site unifi.SiteSettings
	if err := json.Unmarshal(doc, &site); err != nil {
		return unifi.SiteSettings{}, fmt.Errorf("the stored UniFi site settings: %w", err)
	}
	return site, nil
}

// Ensure creates the site settings, generating their unique values, if they do
// not exist yet; otherwise it keeps what was generated. Country, timezones and
// NTP servers in o are the operator's: a non-zero one replaces the stored value.
// It logs what changed but never a value that is secret or generated.
func Ensure(ctx context.Context, st store.Store, o unifi.SiteOptions) error {
	site, err := unifi.NewSite(o)
	if err != nil {
		return err
	}
	doc, err := json.Marshal(site)
	if err != nil {
		return err
	}
	created, err := st.InitAdapterSettings(ctx, unifi.Name, doc)
	if err != nil {
		return err
	}
	if created {
		slog.InfoContext(ctx, "generated the UniFi site settings", slog.Int("country", site.Country))
		return nil
	}
	cur, err := Load(ctx, st)
	if err != nil {
		return err
	}
	next := cur
	if o.Country != 0 {
		next.Country = o.Country
	}
	if o.LocaleTimezone != "" {
		next.LocaleTimezone = o.LocaleTimezone
	}
	if o.SystemTimezone != "" {
		next.SystemTimezone = o.SystemTimezone
	}
	if len(o.NTPServers) > 0 {
		next.NTPServers = o.NTPServers
	}
	if next.Country == cur.Country && next.LocaleTimezone == cur.LocaleTimezone && next.SystemTimezone == cur.SystemTimezone && slices.Equal(next.NTPServers, cur.NTPServers) {
		return nil
	}
	doc, err = json.Marshal(next)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "updated the UniFi site settings from the configuration", slog.Int("country", next.Country))
	return st.SetAdapterSettings(ctx, unifi.Name, doc)
}
