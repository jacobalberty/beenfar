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
	// ErrConflict is returned when a write violates a uniqueness constraint.
	ErrConflict = errors.New("conflict")
	// ErrStale is returned when a revision (etag) check fails: the record
	// changed since the caller read it.
	ErrStale = errors.New("stale revision")
	// ErrFailedPrecondition is returned when the system is not in a state the
	// operation requires, e.g. adopting an already-adopted device or deleting
	// a record that others still refer to.
	ErrFailedPrecondition = errors.New("failed precondition")
	// ErrInvalid is returned when an argument is malformed.
	ErrInvalid = errors.New("invalid argument")
)

// Store is the full persistence API. Implementations must be safe for
// concurrent use.
type Store interface {
	DeviceStore
	WifiNetworkStore
	NetworkStore
	RadiusServerStore
	SwitchPortStore
	SettingsStore
	FieldWriterStore
	UserStore
	APITokenStore
	AdapterTokenStore
	SessionStore

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

// Readoption says where an adopted device that came back without its key (it
// was factory reset) stands. It stays the same adopted device throughout.
type Readoption string

const (
	// ReadoptionNone: nothing to do.
	ReadoptionNone Readoption = ""
	// ReadoptionAwaiting: the device was reset and nobody has accepted
	// re-adopting it yet. Its adapter sends it nothing until then.
	ReadoptionAwaiting Readoption = "awaiting"
	// ReadoptionInProgress: re-adoption was accepted and the device is
	// being given a new key.
	ReadoptionInProgress Readoption = "in_progress"
)

// DeviceKind says what a device is.
type DeviceKind string

const (
	DeviceKindUnknown     DeviceKind = ""
	DeviceKindAccessPoint DeviceKind = "access_point"
	DeviceKindSwitch      DeviceKind = "switch"
	DeviceKindGateway     DeviceKind = "gateway"
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
	// Locked means the adapter has seen the device but holds no credentials
	// for it, so it cannot manage it yet.
	Locked bool
	// Readoption is set while an adopted device is being adopted again.
	Readoption Readoption
	// Forgetting is set while the controller waits for the device's adapter to
	// reset it; the device is dropped once the adapter reports it released.
	Forgetting bool
	// Kind is what the adapter says the device is; empty when it cannot tell.
	Kind DeviceKind
	// PortCount is how many ports beyond the uplink can be configured. Most
	// access points have none. It is meaningful once Kind is known.
	PortCount int
	// APGroup pins an access point to one AP group. Empty is the default: it
	// broadcasts every wifi network that is not limited to named groups.
	APGroup    string
	LastSeen   time.Time
	CreateTime time.Time
	UpdateTime time.Time
}

// DiscoveredDevice is what an adapter reports when it sees a device.
type DiscoveredDevice struct {
	MAC      string
	Adapter  string
	Model    string
	IP       string
	Firmware string
	Kind     DeviceKind
	// PortCount is how many ports beyond the uplink can be configured; read
	// only when Kind is set.
	PortCount int
	Locked    bool
}

// DeviceReport is what an adapter learned from a device it manages. Blank
// strings keep the values already known.
type DeviceReport struct {
	Model          string
	IP             string
	Firmware       string
	Kind           DeviceKind
	PortCount      int // read only when Kind is set
	ConfigRevision int64
	Readoption     Readoption
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
	// AdoptDevice moves a pending device to adopted, or accepts the
	// re-adoption of an adopted device whose readoption is awaiting (it
	// becomes in progress). It returns
	// ErrFailedPrecondition if the device is already adopted.
	AdoptDevice(ctx context.Context, id string) (Device, error)
	// MarkDeviceForgetting marks an adopted device as being forgotten, for its
	// adapter to reset on its next contact. It returns ErrFailedPrecondition
	// for a device that is not adopted.
	MarkDeviceForgetting(ctx context.Context, id string) (Device, error)
	// SetDeviceAPGroup pins a device to an AP group; empty returns it to the
	// default. It returns ErrNotFound for an unknown device.
	SetDeviceAPGroup(ctx context.Context, id, group string) (Device, error)
	// DeleteDevice forgets a device in any state, along with its secrets
	// and switch port settings.
	DeleteDevice(ctx context.Context, id string) error
	// RecordDeviceState stores what the device reported about itself and
	// marks it seen and unlocked.
	RecordDeviceState(ctx context.Context, id string, r DeviceReport) (Device, error)
	// GetDeviceSecret returns an adapter secret of the device, or
	// ErrNotFound.
	GetDeviceSecret(ctx context.Context, deviceID, name string) ([]byte, error)
	// SetDeviceSecret stores a non-empty secret, replacing any with the
	// same name. It returns ErrNotFound for an unknown device.
	SetDeviceSecret(ctx context.Context, deviceID, name string, secret []byte) error
	// DeleteDeviceSecret removes a secret. Removing one that does not exist
	// is not an error.
	DeleteDeviceSecret(ctx context.Context, deviceID, name string) error
}

// DeviceSettings are the settings of a device an operator can change. Their
// names and meaning belong to the device's adapter. Original is the baseline
// built when the device was adopted; Current is what it should run.
type DeviceSettings struct {
	Original map[string]string
	Current  map[string]string
}

// SettingsStore keeps adapter-defined settings.
type SettingsStore interface {
	// GetDeviceSettings returns the settings of a device, or ErrNotFound when
	// none have been stored (or the device is unknown).
	GetDeviceSettings(ctx context.Context, deviceID string) (DeviceSettings, error)
	// SetDeviceSettings replaces the settings of a device. It returns
	// ErrNotFound for an unknown device.
	SetDeviceSettings(ctx context.Context, deviceID string, s DeviceSettings) error
	// GetAdapterSettings returns the JSON document an adapter keeps for
	// itself, or ErrNotFound. It can hold secrets.
	GetAdapterSettings(ctx context.Context, adapter string) ([]byte, error)
	// SetAdapterSettings replaces an adapter's document, which must be a JSON object.
	SetAdapterSettings(ctx context.Context, adapter string, doc []byte) error
	// InitAdapterSettings stores doc only if the adapter has none yet, and
	// reports whether it did. It is how an adapter generates values once, at
	// first start, even when several daemons start together.
	InitAdapterSettings(ctx context.Context, adapter string, doc []byte) (bool, error)
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
	// RadiusProfile is a free-text RADIUS profile name, kept for existing
	// clients; RadiusServerID supersedes it.
	RadiusProfile string
	// RadiusServerID is the RadiusServer that a WPA-enterprise network
	// authenticates against; empty means none.
	RadiusServerID string
	// FieldWriters says who last set each field. It is not stored with
	// the record: core fills it in from FieldWriterStore, and writes ignore it.
	FieldWriters []FieldWriter
	// Revision increases on every update and backs the API's etag.
	Revision   int64
	CreateTime time.Time
	UpdateTime time.Time
}

type WifiNetworkStore interface {
	// CreateWifiNetwork assigns ID, Revision and timestamps. It returns
	// ErrConflict if the SSID is already used, and ErrInvalid if NetworkID or
	// RadiusServerID is set but names no record.
	CreateWifiNetwork(ctx context.Context, w WifiNetwork) (WifiNetwork, error)
	GetWifiNetwork(ctx context.Context, id string) (WifiNetwork, error)
	ListWifiNetworks(ctx context.Context) ([]WifiNetwork, error)
	// UpdateWifiNetwork replaces every mutable field. If w.Revision is
	// non-zero it must equal the stored revision, or ErrStale is returned.
	UpdateWifiNetwork(ctx context.Context, w WifiNetwork) (WifiNetwork, error)
	// DeleteWifiNetwork deletes by ID. If revision is non-zero it must equal
	// the stored revision, or ErrStale is returned.
	DeleteWifiNetwork(ctx context.Context, id string, revision int64) error
}

// Network is a VLAN and its IPv4 subnet.
type Network struct {
	ID          string
	Name        string
	VLANID      int // 0 means untagged
	Subnet      string
	DHCPEnabled bool
	DHCPStart   string
	DHCPStop    string
	DomainName  string
	// FieldWriters says who last set each field. It is not stored with
	// the record: core fills it in from FieldWriterStore, and writes ignore it.
	FieldWriters []FieldWriter
	// Revision increases on every update and backs the API's etag.
	Revision   int64
	CreateTime time.Time
	UpdateTime time.Time
}

type NetworkStore interface {
	// CreateNetwork assigns ID, Revision and timestamps. It returns
	// ErrConflict if the name or a non-zero VLAN id is already used.
	CreateNetwork(ctx context.Context, n Network) (Network, error)
	GetNetwork(ctx context.Context, id string) (Network, error)
	ListNetworks(ctx context.Context) ([]Network, error)
	// UpdateNetwork replaces every mutable field, with the same revision
	// semantics as UpdateWifiNetwork.
	UpdateNetwork(ctx context.Context, n Network) (Network, error)
	// DeleteNetwork deletes by ID, with the same revision semantics as
	// DeleteWifiNetwork. It returns ErrFailedPrecondition while a wireless
	// network or a switch port refers to it.
	DeleteNetwork(ctx context.Context, id string, revision int64) error
}

// RadiusServer is a RADIUS server for WPA-enterprise authentication and,
// optionally, accounting.
type RadiusServer struct {
	ID      string
	Name    string
	Address string // IP address
	// AuthPort is the authentication port, usually 1812.
	AuthPort int
	// AccountingPort is the accounting port, usually 1813; 0 means no
	// accounting.
	AccountingPort int
	SharedSecret   string
	// SecretVersion starts at 1 and increases whenever SharedSecret
	// changes, so the secret's history can be tracked without reading it.
	SecretVersion int64
	// FieldWriters says who last set each field. It is not stored with
	// the record: core fills it in from FieldWriterStore, and writes ignore it.
	FieldWriters []FieldWriter
	// Revision increases on every update and backs the API's etag.
	Revision   int64
	CreateTime time.Time
	UpdateTime time.Time
}

type RadiusServerStore interface {
	// CreateRadiusServer assigns ID, SecretVersion, Revision and timestamps.
	// It returns ErrConflict if the name is already used.
	CreateRadiusServer(ctx context.Context, r RadiusServer) (RadiusServer, error)
	GetRadiusServer(ctx context.Context, id string) (RadiusServer, error)
	ListRadiusServers(ctx context.Context) ([]RadiusServer, error)
	// UpdateRadiusServer replaces every mutable field, with the same revision
	// semantics as UpdateWifiNetwork. SecretVersion increases if the shared
	// secret changed.
	UpdateRadiusServer(ctx context.Context, r RadiusServer) (RadiusServer, error)
	// DeleteRadiusServer deletes by ID, with the same revision semantics as
	// DeleteWifiNetwork. It returns ErrFailedPrecondition while a wireless
	// network refers to it.
	DeleteRadiusServer(ctx context.Context, id string, revision int64) error
}

// FieldResource names a kind of resource whose fields have recorded writers.
type FieldResource string

const (
	FieldResourceWifiNetwork FieldResource = "wifi_network"
	FieldResourceNetwork     FieldResource = "network"
	FieldResourceRadius      FieldResource = "radius_server"
	FieldResourceSwitchPort  FieldResource = "switch_port"
)

// FieldWriterKind says what kind of caller wrote a field.
type FieldWriterKind string

const (
	// FieldWriterUser is a person, through a session, Basic auth or a proxy.
	FieldWriterUser FieldWriterKind = "user"
	// FieldWriterAPIToken is a client using an API token, e.g. Terraform.
	FieldWriterAPIToken FieldWriterKind = "api_token"
)

// FieldWriter records who last set one field of a resource, and when. It is
// evidence of a write, not a claim of ownership.
type FieldWriter struct {
	// Field is the API name of the field, e.g. "ap_groups".
	Field string
	// Writer is the user name or, for an API token, the token's name.
	Writer    string
	Kind      FieldWriterKind
	WriteTime time.Time
}

// FieldWriterStore keeps the last writer of each field of the resources in
// FieldResource. Rows disappear with their resource.
type FieldWriterStore interface {
	// RecordFieldWriters makes w (its Field and WriteTime are ignored; the
	// store stamps the time) the last writer of each of fields.
	RecordFieldWriters(ctx context.Context, resource FieldResource, id string, fields []string, w FieldWriter) error
	// GetFieldWriters returns the writers of one resource, ordered by field.
	GetFieldWriters(ctx context.Context, resource FieldResource, id string) ([]FieldWriter, error)
	// ListFieldWriters returns the writers of every resource of a kind,
	// by resource id.
	ListFieldWriters(ctx context.Context, resource FieldResource) (map[string][]FieldWriter, error)
}

// SwitchPortTagging says which networks a switch port carries tagged.
type SwitchPortTagging string

const (
	SwitchPortTaggingAll    SwitchPortTagging = "all"    // every network except the native one
	SwitchPortTaggingNone   SwitchPortTagging = "none"   // only the native network, untagged
	SwitchPortTaggingCustom SwitchPortTagging = "custom" // the networks in TaggedNetworkIDs
)

// PoEMode is the Power over Ethernet output of a switch port.
type PoEMode string

const (
	PoEModeAuto PoEMode = "auto"
	PoEModeOff  PoEMode = "off"
)

// SwitchPort holds the settings of one port of an adopted switch. A port
// with no SwitchPort runs with the device's defaults.
type SwitchPort struct {
	ID       string
	DeviceID string // immutable
	// PortIndex is 1-based, as printed on the device. Immutable.
	PortIndex int
	Name      string
	Disabled  bool
	// NativeNetworkID is the untagged network; empty means the default
	// network.
	NativeNetworkID  string
	Tagging          SwitchPortTagging
	TaggedNetworkIDs []string // sorted; only with SwitchPortTaggingCustom
	PoEMode          PoEMode
	// FieldWriters says who last set each field. It is not stored with the
	// record: core fills it in from FieldWriterStore, and writes ignore it.
	FieldWriters []FieldWriter
	// Revision increases on every update and backs the API's etag.
	Revision   int64
	CreateTime time.Time
	UpdateTime time.Time
}

type SwitchPortStore interface {
	// CreateSwitchPort assigns ID, Revision and timestamps. It returns
	// ErrConflict if the device's port already has settings, and ErrInvalid
	// if the device or a network does not exist.
	CreateSwitchPort(ctx context.Context, p SwitchPort) (SwitchPort, error)
	GetSwitchPort(ctx context.Context, id string) (SwitchPort, error)
	// ListSwitchPorts lists the ports of one device, or of all devices when
	// deviceID is empty, ordered by device and port index.
	ListSwitchPorts(ctx context.Context, deviceID string) ([]SwitchPort, error)
	// UpdateSwitchPort replaces every mutable field (not DeviceID or
	// PortIndex), with the same revision semantics as UpdateWifiNetwork.
	UpdateSwitchPort(ctx context.Context, p SwitchPort) (SwitchPort, error)
	// DeleteSwitchPort deletes by ID, with the same revision semantics as
	// DeleteWifiNetwork, returning the port to the device's defaults.
	DeleteSwitchPort(ctx context.Context, id string, revision int64) error
}

// UserSource says where a user account comes from.
type UserSource string

const (
	UserSourceLocal UserSource = "local" // created in beenfar; has a password
	UserSourceProxy UserSource = "proxy" // created from trusted proxy headers
	UserSourceOIDC  UserSource = "oidc"  // created on first OIDC sign-in
)

// User is a person who can sign in.
type User struct {
	ID       string
	Username string
	// PasswordHash is encoded by internal/auth and never leaves the server.
	// It is empty for external users, who cannot sign in with a password.
	PasswordHash string
	Admin        bool
	Source       UserSource // empty means UserSourceLocal on create
	Revision     int64
	CreateTime   time.Time
	UpdateTime   time.Time
}

type UserStore interface {
	// CreateUser assigns ID, Revision and timestamps. It returns ErrConflict
	// if the username is taken.
	CreateUser(ctx context.Context, u User) (User, error)
	GetUser(ctx context.Context, id string) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	// GetUserByIdentity finds the user linked to an external identity.
	GetUserByIdentity(ctx context.Context, provider, subject string) (User, error)
	// CreateExternalUser creates a user without a password (u.Source must not
	// be local) and links it to the external identity (provider, subject). It
	// returns ErrConflict if the username or the identity is taken.
	CreateExternalUser(ctx context.Context, u User, provider, subject string) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	CountUsers(ctx context.Context) (int64, error)
	CountAdmins(ctx context.Context) (int64, error)
	// UpdateUser replaces Username and Admin (not the password), with the
	// same revision semantics as UpdateWifiNetwork.
	UpdateUser(ctx context.Context, u User) (User, error)
	// SetUserPassword replaces the password hash. It does not change Revision.
	SetUserPassword(ctx context.Context, id, passwordHash string) error
	// DeleteUser deletes a user with their API tokens and sessions, with the
	// same revision semantics as DeleteWifiNetwork.
	DeleteUser(ctx context.Context, id string, revision int64) error
}

// APIToken is an issued bearer token. Only a hash of the secret is stored.
type APIToken struct {
	ID           string
	UserID       string
	Name         string
	Prefix       string // unique, used to find the token
	SecretHash   string
	ExpireTime   time.Time // zero means never
	LastUsedTime time.Time // zero means never used
	CreateTime   time.Time
}

type APITokenStore interface {
	// CreateAPIToken assigns ID and CreateTime.
	CreateAPIToken(ctx context.Context, t APIToken) (APIToken, error)
	GetAPIToken(ctx context.Context, id string) (APIToken, error)
	GetAPITokenByPrefix(ctx context.Context, prefix string) (APIToken, error)
	// ListAPITokens lists one user's tokens, or everyone's if userID is empty.
	ListAPITokens(ctx context.Context, userID string) ([]APIToken, error)
	TouchAPIToken(ctx context.Context, id string, at time.Time) error
	DeleteAPIToken(ctx context.Context, id string) error
}

// AdapterToken is a credential of an external adapter. Only a hash of the
// secret is stored.
type AdapterToken struct {
	ID           string
	Adapter      string // the adapter the token may act as
	Prefix       string // unique, used to find the token
	SecretHash   string
	LastUsedTime time.Time // zero means never used
	CreateTime   time.Time
}

type AdapterTokenStore interface {
	// CreateAdapterToken assigns ID and CreateTime.
	CreateAdapterToken(ctx context.Context, t AdapterToken) (AdapterToken, error)
	GetAdapterTokenByPrefix(ctx context.Context, prefix string) (AdapterToken, error)
	ListAdapterTokens(ctx context.Context) ([]AdapterToken, error)
	TouchAdapterToken(ctx context.Context, id string, at time.Time) error
	DeleteAdapterToken(ctx context.Context, id string) error
}

// Session is a signed-in browser session. Only a hash of the cookie is stored.
type Session struct {
	IDHash string
	UserID string
	// Provider records how the session started: empty for password login,
	// "oidc:<provider id>" for OIDC.
	Provider   string
	CreateTime time.Time
	ExpireTime time.Time
}

type SessionStore interface {
	// CreateSession assigns CreateTime.
	CreateSession(ctx context.Context, s Session) (Session, error)
	GetSession(ctx context.Context, idHash string) (Session, error)
	DeleteSession(ctx context.Context, idHash string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	// DeleteExpiredSessions removes sessions that expired at or before now and
	// returns how many it removed.
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error)
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
