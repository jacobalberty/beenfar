// Package model holds the plain data types adapters and the controller
// exchange. They are declared here, and aliased by package adapter, so that
// the generated protobuf conversions can depend on them without importing
// package adapter (which uses those conversions in RunRemote).
package model

import "time"

// DeviceState is the adoption state of a device.
type DeviceState string

const (
	DevicePending DeviceState = "pending"
	DeviceAdopted DeviceState = "adopted"
)

// Device is a device as the controller knows it.
type Device struct {
	ID    string
	MAC   string // canonical lower-case, colon-separated
	State DeviceState
	// ConfigRevision is the revision of the configuration last pushed to the
	// device.
	ConfigRevision int64
}

// Discovered is what an adapter reports when it sees a device.
type Discovered struct {
	MAC      string
	Model    string
	IP       string
	Firmware string
	// Locked marks a device the adapter cannot talk to yet because it lacks
	// the device's credentials. It clears when the adapter reports the
	// device again without it.
	Locked bool
}

// State is what an adapter reports about a device it manages.
type State struct {
	IP       string
	Firmware string
	Model    string
	// ConfigRevision is the revision the device says it is running.
	ConfigRevision int64
	// Uptime is how long the device has been up.
	Uptime time.Duration
}

// DesiredConfig is the configuration for one device.
type DesiredConfig struct {
	// Revision changes whenever Networks or Wifi change.
	Revision int64
	Networks []Network
	Wifi     []WifiNetwork
}

// Network is a resolved network (a VLAN and its addressing).
type Network struct {
	ID          string
	Name        string
	VLANID      int // 0 means untagged
	Subnet      string
	DHCPEnabled bool
	DHCPStart   string
	DHCPStop    string
	DomainName  string
}

// WifiSecurity is how a wifi network authenticates clients.
type WifiSecurity string

const (
	WifiOpen          WifiSecurity = "open"
	WifiWEP           WifiSecurity = "wep"
	WifiWPAPersonal   WifiSecurity = "wpa_personal"
	WifiWPAEnterprise WifiSecurity = "wpa_enterprise"
)

// WifiBand says which radios carry a wifi network.
type WifiBand string

const (
	BandBoth WifiBand = "both"
	Band2G   WifiBand = "2g"
	Band5G   WifiBand = "5g"
)

// WifiNetwork is a resolved wifi network.
type WifiNetwork struct {
	ID          string
	SSID        string
	Security    WifiSecurity
	SecurityKey string
	Band        WifiBand
	// NetworkID is the network the SSID's clients join; empty means the
	// default one.
	NetworkID string
	Guest     bool
	Hidden    bool
}
