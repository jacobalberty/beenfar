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
	Locked     bool
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
	Locked   bool
}

// DeviceReport is what an adapter learned from a device it manages. Blank
// strings keep the values already known.
type DeviceReport struct {
	Model          string
	IP             string
	Firmware       string
	ConfigRevision int64
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
	// AdoptDevice moves a pending device to adopted. It returns
	// ErrFailedPrecondition if the device is already adopted.
	AdoptDevice(ctx context.Context, id string) (Device, error)
	// DeleteDevice forgets a device in any state, along with its secrets.
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
	// ErrConflict if the SSID is already used, and ErrInvalid if NetworkID is
	// set but names no network.
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
	// network refers to it.
	DeleteNetwork(ctx context.Context, id string, revision int64) error
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
