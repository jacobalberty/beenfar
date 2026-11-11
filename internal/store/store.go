// Package store defines the persistence boundary for beenfar.
//
// Core code depends only on the Store interface. Each database dialect
// (store/sqlite today) provides an implementation, and decorators such as
// store/otelstore wrap any implementation.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrNotFound is returned when the requested record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned when a write violates a uniqueness constraint,
	// when a revision (etag) check fails, or when a state transition is invalid.
	ErrConflict = errors.New("conflict")
	// ErrInvalid is returned when an argument is malformed.
	ErrInvalid = errors.New("invalid argument")
)

// Store is the full persistence API. Implementations must be safe for
// concurrent use.
type Store interface {
	DeviceStore
	WifiNetworkStore

	// WithTx runs fn inside a transaction. The Store passed to fn is bound to
	// the transaction; fn must not use the outer Store. The transaction is
	// committed if fn returns nil and rolled back otherwise.
	WithTx(ctx context.Context, fn func(Store) error) error

	// Ping checks that the database is reachable.
	Ping(ctx context.Context) error

	// Close releases the database. Calling Close on a transaction-bound
	// Store is a no-op.
	Close() error
}

// DeviceState is the adoption state of a device.
type DeviceState string

const (
	DeviceStatePending DeviceState = "pending"
	DeviceStateAdopted DeviceState = "adopted"
)

// Device is a piece of network gear known to the controller.
type Device struct {
	ID       string
	MAC      string // canonical form, see NormalizeMAC
	Adapter  string // name of the adapter that handles this device
	State    DeviceState
	Model    string
	IP       string
	Firmware string
	// ConfigRevision is the revision of the configuration last pushed to the device.
	ConfigRevision int64
	LastSeen       time.Time
	CreateTime     time.Time
	UpdateTime     time.Time
}

// DiscoveredDevice is what an adapter reports when it sees a device.
type DiscoveredDevice struct {
	MAC      string
	Adapter  string
	Model    string
	IP       string
	Firmware string
}

// DeviceFilter narrows ListDevices. Zero values match everything.
type DeviceFilter struct {
	State   DeviceState
	Adapter string
}

type DeviceStore interface {
	// ReportDevice records that a device was seen. A new MAC is created in the
	// pending state; a known one has its last-seen time and details updated.
	ReportDevice(ctx context.Context, d DiscoveredDevice) (Device, error)
	GetDevice(ctx context.Context, id string) (Device, error)
	GetDeviceByMAC(ctx context.Context, mac string) (Device, error)
	ListDevices(ctx context.Context, f DeviceFilter) ([]Device, error)
	// AdoptDevice moves a pending device to adopted. It returns ErrConflict if
	// the device is already adopted.
	AdoptDevice(ctx context.Context, id string) (Device, error)
	// DeleteDevice forgets a device in any state.
	DeleteDevice(ctx context.Context, id string) error
}

type WifiSecurity string

const (
	WifiSecurityOpen          WifiSecurity = "open"
	WifiSecurityWEP           WifiSecurity = "wep"
	WifiSecurityWPAPersonal   WifiSecurity = "wpa_personal"
	WifiSecurityWPAEnterprise WifiSecurity = "wpa_enterprise"
)

type WifiBand string

const (
	WifiBandBoth WifiBand = "both"
	WifiBand2G   WifiBand = "2g"
	WifiBand5G   WifiBand = "5g"
)

// WifiNetwork is a wireless network definition.
type WifiNetwork struct {
	ID               string
	SSID             string
	Security         WifiSecurity
	SecurityKey      string
	Band             WifiBand
	NetworkID        string // optional; empty means the default network
	Guest            bool
	Hidden           bool
	APGroups         []string
	DefaultUserGroup string
	RadiusProfile    string
	// Revision increases on every update and backs the API's etag.
	Revision   int64
	CreateTime time.Time
	UpdateTime time.Time
}

type WifiNetworkStore interface {
	// CreateWifiNetwork assigns ID, Revision and timestamps. It returns
	// ErrConflict if the SSID is already used.
	CreateWifiNetwork(ctx context.Context, w WifiNetwork) (WifiNetwork, error)
	GetWifiNetwork(ctx context.Context, id string) (WifiNetwork, error)
	ListWifiNetworks(ctx context.Context) ([]WifiNetwork, error)
	// UpdateWifiNetwork replaces every mutable field. If w.Revision is
	// non-zero it must equal the stored revision, or ErrConflict is returned.
	UpdateWifiNetwork(ctx context.Context, w WifiNetwork) (WifiNetwork, error)
	// DeleteWifiNetwork deletes by ID. If revision is non-zero it must equal
	// the stored revision, or ErrConflict is returned.
	DeleteWifiNetwork(ctx context.Context, id string, revision int64) error
}

// NormalizeMAC returns mac as lower-case, colon-separated hex
// ("aa:bb:cc:dd:ee:ff"). It accepts colon, dash, dot or no separators.
func NormalizeMAC(mac string) (string, error) {
	var hex []byte
	for i := 0; i < len(mac); i++ {
		c := mac[i]
		switch {
		case c == ':' || c == '-' || c == '.':
			continue
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			hex = append(hex, c)
		case c >= 'A' && c <= 'F':
			hex = append(hex, c+('a'-'A'))
		default:
			return "", fmt.Errorf("%w: mac %q", ErrInvalid, mac)
		}
	}
	if len(hex) != 12 {
		return "", fmt.Errorf("%w: mac %q", ErrInvalid, mac)
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.Write(hex[i : i+2])
	}
	return b.String(), nil
}
