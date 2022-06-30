package model

import "errors"

var (
	ErrDuplicateSsid = errors.New("duplicate ssid")
)

// Enum of supported WiFi security modes
type WifiSecurityType int

const (
	WifiSecurityTypeOpen WifiSecurityType = iota
	WifiSecurityTypeWep
	WifiSecurityTypeWpaPersonal
	WifiSecurityTypeWpaEnterprise
)

// Enum of supported WiFi band modes
type WifiBand int

const (
	WifiBandBoth WifiBand = iota
	WifiBand2G
	WifiBand5G
)

type WifiApGroup int

type WifiUserGroup int

type RadiusProfileID int

type NetworkID int

// This is the model for the WiFi configuration of an access point
type WifiNetworkConfig struct {
	Ssid             string           `jsonapi:"primary,ssid"`
	SecurityType     WifiSecurityType `jsonapi:"attr,security_type"`
	SecurityKey      string           `jsonapi:"attr,security_key,omitempty"`
	Band             WifiBand         `jsonapi:"attr,band"`
	Network          NetworkID        `jsonapi:"attr,network,omitempty"`
	Guest            bool             `jsonapi:"attr,guest"`
	APGroups         []WifiApGroup    `jsonapi:"attr,ap_groups,omitempty"`
	Hidden           bool             `jsonapi:"attr,hidden"`
	DefaultUserGroup WifiUserGroup    `jsonapi:"attr,default_user_group,omitempty"`
	RadiusProfile    RadiusProfileID  `jsonapi:"attr,radius_profile,omitempty"`
}

type NetworkPurpose int

const (
	NetworkPurposeCorporate NetworkPurpose = iota
	NetworkPurposeGuest
)

type NetworkConfig struct {
	Name              string            `jsonapi:"attr,name"`
	Purpose           NetworkPurpose    `jsonapi:"attr,purpose"`
	Interface         int               `jsonapi:"attr,interface"`
	GatewayIPSubnet   string            `jsonapi:"attr,gateway_ip_subnet"`
	DomainName        string            `jsonapi:"attr,domain_name"`
	DHCPConfig        DHCPConfig        `jsonapi:"attr,dhcp_config"`
	IPV6NetworkConfig IPV6NetworkConfig `jsonapi:"attr,ipv6_network_config"`
}

type DHCPConfig struct {
	DHCPMode       DHCPMode       `jsonapi:"attr,dhcp_mode"`
	DHCPRange      [2]string      `jsonapi:"attr,dhcp_range"`
	DHCPNameServer DHCPNameServer `jsonapi:"attr,dhcp_name_server"`
	DHCPLeaseTime  int            `jsonapi:"attr,dhcp_lease_time"`
	DHCPGateway    DHCPGateway    `jsonapi:"attr,dhcp_gateway"`
}

type DHCPMode int

const (
	DHCPModeDisabled DHCPMode = iota
	DHCPModeServer
	DHCPModeRelay
)

type DHCPNameServer struct {
	Auto      bool     `jsonapi:"attr,auto"`
	Addresses []string `jsonapi:"attr,addresses,omitempty"`
}

type DHCPGateway struct {
	Auto    bool   `jsonapi:"attr,auto"`
	Address string `jsonapi:"attr,address,omitempty"`
}

type IPV6NetworkConfig struct {
	Type                      string   `jsonapi:"attr,type"`
	PrefixDelegationInterface int      `jsonapi:"attr,prefix_delegation_interface"`
	PrefixID                  int      `jsonapi:"attr,prefix_id"`
	RAEnabled                 bool     `jsonapi:"attr,ra_enabled"`
	RAPriority                int      `jsonapi:"attr,ra_priority"`
	RAValidLifetime           int      `jsonapi:"attr,ra_valid_lifetime"`
	RAPrefferedLifetime       int      `jsonapi:"attr,ra_preferred_lifetime"`
	RDNSSControlAuto          bool     `jsonapi:"attr,rdnss_control_auto"`
	RDNSSNameServers          []string `jsonapi:"attr,rdnss_name_servers,omitempty"`
}
