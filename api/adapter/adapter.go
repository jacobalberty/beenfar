// Package adapter is the SDK for beenfar device adapters.
//
// An adapter speaks one vendor's device protocol (UniFi, for example) and
// turns it into the controller's vendor-neutral model: it reports the
// devices it sees, receives adopt and forget commands, and reads the
// configuration the controller wants each device to run.
//
// The same adapter code runs two ways. Built-in adapters are compiled into
// beenfard and get an in-process Host. External adapters run as their own
// process and use RunRemote, which gives them a Host backed by the adapter
// API.
//
// Every method takes a context. Built-in adapters therefore log and trace
// under the caller's span, and RunRemote carries the trace across the
// process boundary.
package adapter

import (
	"context"
	"errors"
	"net/http"

	"github.com/jacobalberty/beenfar/api/adapter/model"
)

// Errors an adapter or Host returns. Hosts map them onto the transport's own
// status codes, so callers can test for them with errors.Is on either side of
// the adapter API.
var (
	// ErrNotFound means the device (or adapter) is not known to the controller.
	ErrNotFound = errors.New("adapter: not found")
	// ErrUnknownDevice means the controller does not manage the device, e.g.
	// it is still pending. Adapters answer such a device as "not adopted".
	ErrUnknownDevice = errors.New("adapter: unknown device")
	// ErrNoSecret means the device has no secret under that name.
	ErrNoSecret = errors.New("adapter: no such device secret")
)

// Adapter is a device adapter.
type Adapter interface {
	// Name is the adapter's unique name, e.g. "unifi". It is recorded on every
	// device the adapter reports.
	Name() string
	// Start runs the adapter until ctx is done. It returns nil on a clean
	// shutdown. host stays valid until Start returns.
	Start(ctx context.Context, host Host) error
	// Adopt tells the adapter that the controller now manages d. The
	// adapter takes the device over (for UniFi: re-keys it). An error fails
	// the adoption and leaves the device pending.
	Adopt(ctx context.Context, d Device) error
	// Forget tells the adapter that the controller no longer manages d.
	Forget(ctx context.Context, d Device) error
}

// HTTPProvider is implemented by built-in adapters that serve device-facing
// HTTP endpoints, such as UniFi's /inform. The map keys are ServeMux
// patterns, e.g. "POST /inform". External adapters run their own listener
// and do not implement it.
type HTTPProvider interface {
	HTTPHandlers() map[string]http.Handler
}

// Host is what an adapter may ask of the controller.
type Host interface {
	// DeviceByMAC returns the device with that MAC, or ErrNotFound. Adapters
	// use it before a device is decoded, when only the MAC is known, e.g. to
	// find the secrets to try.
	DeviceByMAC(ctx context.Context, mac string) (Device, error)
	// ReportDevice records that a device was seen. A new device becomes
	// pending; a known one is refreshed. It returns the device as the
	// controller knows it.
	ReportDevice(ctx context.Context, d Discovered) (Device, error)
	// DesiredConfig returns the configuration the controller wants the
	// device to run: the resolved networks and wifi networks plus a revision
	// that changes whenever any of them does.
	DesiredConfig(ctx context.Context, deviceID string) (DesiredConfig, error)
	// ReportState records what the device reported about itself.
	ReportState(ctx context.Context, deviceID string, s State) error
	// DeviceSecret returns the adapter's secret named name for the device,
	// or ErrNoSecret. Secrets are private to the adapter that stored them
	// and are never exposed by the public API.
	DeviceSecret(ctx context.Context, deviceID, name string) ([]byte, error)
	// SetDeviceSecret stores a secret for the device, replacing any with the
	// same name.
	SetDeviceSecret(ctx context.Context, deviceID, name string, secret []byte) error
}

// The data types live in package model; these aliases make them part of the
// adapter API.
type (
	DeviceState   = model.DeviceState
	Device        = model.Device
	Discovered    = model.Discovered
	State         = model.State
	DesiredConfig = model.DesiredConfig
	Network       = model.Network
	WifiSecurity  = model.WifiSecurity
	WifiBand      = model.WifiBand
	WifiNetwork   = model.WifiNetwork
)

const (
	DevicePending = model.DevicePending
	DeviceAdopted = model.DeviceAdopted

	WifiOpen          = model.WifiOpen
	WifiWEP           = model.WifiWEP
	WifiWPAPersonal   = model.WifiWPAPersonal
	WifiWPAEnterprise = model.WifiWPAEnterprise

	BandBoth = model.BandBoth
	Band2G   = model.Band2G
	Band5G   = model.Band5G
)
