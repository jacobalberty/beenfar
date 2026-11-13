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

// Readoption says where an adopted device that came back without its key (it
// was factory reset) stands. It stays the same adopted device throughout.
type Readoption string

const (
	// ReadoptionNone: nothing to do.
	ReadoptionNone Readoption = ""
	// ReadoptionAwaiting: the device was reset and nobody has accepted
	// re-adopting it yet. An adapter must send it nothing until then.
	ReadoptionAwaiting Readoption = "awaiting"
	// ReadoptionInProgress: re-adoption was accepted; the device is being
	// given a new key.
	ReadoptionInProgress Readoption = "in_progress"
)

// DeviceKind says what a device is. The zero value means the adapter cannot
// tell.
type DeviceKind string

const (
	KindUnknown     DeviceKind = ""
	KindAccessPoint DeviceKind = "access_point"
	KindSwitch      DeviceKind = "switch"
	KindGateway     DeviceKind = "gateway"
)

// Device is a device as the controller knows it.
type Device struct {
	ID    string
	MAC   string // canonical lower-case, colon-separated
	State DeviceState
	// ConfigRevision is the revision of the configuration last pushed to the
	// device.
	ConfigRevision int64
	// LastSeen is when the controller last heard from the device through its
	// adapter. Reports that could not be authenticated do not count.
	LastSeen time.Time
	// Readoption is set while an adopted device is being adopted again.
	Readoption Readoption
	// Forgetting is set while the controller is waiting for the adapter to
	// reset the device (see adapter.ErrForgetDeferred).
	Forgetting bool
}

// Discovered is what an adapter reports when it sees a device.
type Discovered struct {
	MAC      string
	Model    string
	IP       string
	Firmware string
	// Kind is what the device is, when the adapter can tell (UniFi: from the
	// model). PortCount is how many ports beyond its uplink can be configured
	// (most access points have none); it is only read when Kind is set.
	Kind      DeviceKind
	PortCount int
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
	// Kind and PortCount are as in Discovered; blank Kind keeps what is known.
	Kind      DeviceKind
	PortCount int
	// ConfigRevision is the revision the device says it is running.
	ConfigRevision int64
	// Uptime is how long the device has been up.
	Uptime time.Duration
	// Readoption is what the adapter found out about the device's
	// readoption: awaiting when a reset device showed up, in progress while
	// it is being given a new key, none once it runs on its key again.
	Readoption Readoption
	// Released says that a device the controller is forgetting has been reset
	// and no longer needs to be managed: the controller drops it. It is
	// ignored for any other device.
	Released bool
}

// DesiredConfig is the configuration for one device.
type DesiredConfig struct {
	// Revision changes whenever anything below changes.
	Revision      int64
	Networks      []Network
	Wifi          []WifiNetwork
	RadiusServers []RadiusServer
	// SwitchPorts are the settings of this device's ports, ordered by
	// PortIndex. Ports not listed run with the device's defaults.
	SwitchPorts []SwitchPort
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
	// RadiusServerID is the RADIUS server a WPA-enterprise network
	// authenticates against, one of DesiredConfig.RadiusServers.
	RadiusServerID string
}

// RadiusServer is a RADIUS server for WPA-enterprise authentication and,
// when AccountingPort is non-zero, accounting.
type RadiusServer struct {
	ID             string
	Address        string // IP address
	AuthPort       int
	AccountingPort int // 0 means no accounting
	SharedSecret   string
}

// SwitchPortTagging says which networks a switch port carries tagged.
type SwitchPortTagging string

const (
	TaggingAll    SwitchPortTagging = "all"    // every network except the native one
	TaggingNone   SwitchPortTagging = "none"   // only the native network, untagged
	TaggingCustom SwitchPortTagging = "custom" // the networks in TaggedNetworkIDs
)

// PoEMode is the Power over Ethernet output of a switch port.
type PoEMode string

const (
	PoEAuto PoEMode = "auto"
	PoEOff  PoEMode = "off"
)

// SwitchPort holds the settings of one port of the device.
type SwitchPort struct {
	PortIndex int // 1-based
	Name      string
	Disabled  bool
	// NativeNetworkID is the untagged network; empty means the default one.
	NativeNetworkID  string
	Tagging          SwitchPortTagging
	TaggedNetworkIDs []string
	PoEMode          PoEMode
}
