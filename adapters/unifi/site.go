package unifi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jacobalberty/go-unifi/inform/profile"
)

// SettingsSource gives the adapter the settings it keeps in the controller's
// database. The daemon implements it on its store; the adapter does not
// reach the store itself, so that it stays an ordinary adapter.
type SettingsSource interface {
	// Site returns the site settings, which are generated at first start.
	Site(ctx context.Context) (SiteSettings, error)
	// DeviceSettings returns the current tunable settings of a device
	// (profile.Profile tunables, by key), or none if nothing was stored: a
	// device with none runs the profile's defaults.
	DeviceSettings(ctx context.Context, deviceID string) (map[string]string, error)
}

// SiteSettings is the controller-wide part of an access point's system.cfg:
// who the device reports to, how to log in to it, and where it gets the time.
// Some of it is secret (AdminPassword, ControllerKey, MeshPSK) or generated
// once and then kept (every id): it is stored by the daemon, never logged and
// never returned by the API.
type SiteSettings struct {
	// Country is the ISO 3166-1 numeric country code the radios run under
	// (840 is the United States). It has no default: a wrong one is a
	// regulatory problem. While it is 0 no access point is sent its settings.
	Country         int      `json:"country"`
	LocaleTimezone  string   `json:"locale_timezone"`
	SystemTimezone  string   `json:"system_timezone"`
	Analytics       bool     `json:"analytics"`
	SSHEnabled      bool     `json:"ssh_enabled"`
	SSHPasswordAuth bool     `json:"ssh_password_auth"`
	NTPServers      []string `json:"ntp_servers"`
	SyslogLevel     int      `json:"syslog_level"`

	// What the controller says it is. ControllerVersion is the version of the
	// UniFi controller whose behaviour beenfar presents; it is not beenfar's.
	ControllerVersion string `json:"controller_version"`
	CfgCapInfo        string `json:"cfgcap_info"`
	IDP               bool   `json:"idp"`
	CrashLogs         bool   `json:"crash_logs"`
	// InformURL is where devices are told to inform. Empty means the address
	// the inform that is being answered arrived on.
	InformURL string `json:"inform_url,omitempty"`

	// Generated at first start.
	ControllerKey         string `json:"controller_key"`
	SiteID                string `json:"site_id"`
	ReporterID            string `json:"reporter_id"`
	AnonymousSiteID       string `json:"anonymous_site_id"`
	AnonymousControllerID string `json:"anonymous_controller_id"`
	MeshESSID             string `json:"mesh_essid"`
	MeshPSK               string `json:"mesh_psk"`
	MeshVersion           int    `json:"mesh_version"`
	AdminUser             string `json:"admin_user"`
	AdminPassword         string `json:"admin_password"`
	AdminPasswordHash     string `json:"admin_password_hash"`
}

// SiteOptions are the operator-chosen parts of a new SiteSettings. A zero
// value takes the default.
type SiteOptions struct {
	// Country is required to provision access points, and not defaulted.
	Country        int
	LocaleTimezone string   // default "UTC"
	SystemTimezone string   // default "UTC"
	NTPServers     []string // default pool.ntp.org
}

// Default values of a new site. Where they come from, and what is not
// verified, is documented in the README.
const (
	DefaultControllerVersion = "10.6.109" // the controller version of the capture the profile came from
	DefaultCfgCapInfo        = "0x7"
	DefaultSyslogLevel       = 5
	DefaultMeshVersion       = 3
	DefaultAdminUser         = "admin"
)

// NewSite generates a site: fresh ids, keys and device admin password.
func NewSite(o SiteOptions) (SiteSettings, error) {
	s := SiteSettings{
		Country:         o.Country,
		LocaleTimezone:  cmpOr(o.LocaleTimezone, "UTC"),
		SystemTimezone:  cmpOr(o.SystemTimezone, "UTC"),
		SSHEnabled:      true,
		SSHPasswordAuth: true,
		NTPServers:      o.NTPServers,
		SyslogLevel:     DefaultSyslogLevel,

		ControllerVersion: DefaultControllerVersion,
		CfgCapInfo:        DefaultCfgCapInfo,
		IDP:               true,
		MeshVersion:       DefaultMeshVersion,
		AdminUser:         DefaultAdminUser,
	}
	if len(s.NTPServers) == 0 {
		s.NTPServers = []string{"pool.ntp.org"}
	}
	var err error
	gen := func(dst *string, n int) {
		if err == nil {
			*dst, err = randomHex(n)
		}
	}
	var meshSuffix, salt string
	gen(&s.ControllerKey, 16)
	gen(&s.SiteID, 12)
	gen(&s.ReporterID, 16)
	gen(&s.AnonymousSiteID, 16)
	gen(&s.AnonymousControllerID, 16)
	gen(&s.MeshPSK, 16)
	gen(&meshSuffix, 4)
	gen(&s.AdminPassword, 12)
	gen(&salt, 8)
	if err != nil {
		return SiteSettings{}, err
	}
	s.MeshESSID = "beenfar-" + meshSuffix
	if s.AdminPasswordHash, err = sha512Crypt(s.AdminPassword, salt); err != nil {
		return SiteSettings{}, err
	}
	return s, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("unifi: generating a site value: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// profileSite maps the site onto go-unifi's input. informHost is the address
// the inform being answered arrived on, used when InformURL is not set.
func (s SiteSettings) profileSite(informHost string) (profile.Site, error) {
	if s.Country == 0 {
		return profile.Site{}, errors.New("no country is configured for the radios (set BEENFAR_UNIFI_COUNTRY)")
	}
	mcip := s.InformURL
	if mcip == "" {
		if informHost == "" {
			return profile.Site{}, errors.New("the controller's inform address is unknown")
		}
		mcip = "http://" + informHost + "/inform"
	}
	if strings.TrimSpace(s.AdminPasswordHash) == "" {
		return profile.Site{}, errors.New("the site has no device admin password")
	}
	return profile.Site{
		Country:        s.Country,
		LocaleTimezone: s.LocaleTimezone,
		SystemTimezone: s.SystemTimezone,
		Analytics:      s.Analytics,
		Users:          []profile.User{{Name: s.AdminUser, PasswordHash: s.AdminPasswordHash, Shell: "/bin/sh"}},
		SSH:            profile.SSH{Enabled: s.SSHEnabled, PasswordAuth: s.SSHPasswordAuth, Interfaces: []string{"br0"}},
		NTPServers:     s.NTPServers,
		Syslog:         profile.Syslog{Level: s.SyslogLevel},
		Controller: profile.Controller{
			Version: s.ControllerVersion, CfgCapInfo: s.CfgCapInfo, IDP: s.IDP, CrashLogs: s.CrashLogs,
			Key: s.ControllerKey, MCIP: mcip, SiteID: s.SiteID, ReporterID: s.ReporterID,
			SiteAnonID: s.AnonymousSiteID, AnonCtlrID: s.AnonymousControllerID,
		},
		Mesh: profile.Mesh{Enabled: false, ESSID: s.MeshESSID, PSK: s.MeshPSK, Version: s.MeshVersion},
	}, nil
}
