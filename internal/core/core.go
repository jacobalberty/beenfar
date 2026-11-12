// Package core holds beenfar's configuration services: the validation and
// cross-resource rules that sit between the API handlers and the store.
//
// Core uses store types as its domain model and knows nothing about
// protobuf or HTTP, so the same services back every transport.
package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jacobalberty/beenfar/internal/store"
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", store.ErrInvalid, fmt.Sprintf(format, args...))
}

// Services bundles the core services over one store.
type Services struct {
	Wifi     *WifiService
	Networks *NetworkService
	Radius   *RadiusService
	Ports    *SwitchPortService
	Devices  *DeviceService
	Adapters AdapterRegistry
}

// New returns the core services. A nil registry reports no adapters.
func New(st store.Store, adapters AdapterRegistry) *Services {
	if adapters == nil {
		adapters = NoAdapters{}
	}
	return &Services{
		Wifi:     &WifiService{store: st},
		Networks: &NetworkService{store: st},
		Radius:   &RadiusService{store: st},
		Ports:    &SwitchPortService{store: st},
		Devices:  &DeviceService{store: st, adapters: adapters},
		Adapters: adapters,
	}
}

// --- adapters ---

// AdapterKind says where an adapter runs.
type AdapterKind string

const (
	AdapterBuiltin  AdapterKind = "builtin"
	AdapterExternal AdapterKind = "external"
)

// AdapterStatus describes a registered device adapter.
type AdapterStatus struct {
	Name          string
	Kind          AdapterKind
	Version       string
	Healthy       bool
	StatusMessage string
	ConnectTime   time.Time
}

// AdapterRegistry lists registered adapters and hands devices to them. The
// adapter host implements it.
type AdapterRegistry interface {
	ListAdapters(ctx context.Context) []AdapterStatus
	// AdoptDevice asks the device's adapter to take the device over. It
	// returns ErrFailedPrecondition if that adapter is not connected.
	AdoptDevice(ctx context.Context, d store.Device) error
	// ForgetDevice tells the device's adapter the device is no longer managed.
	// deferred is true when the adapter will reset the device on its next
	// contact and the controller must keep the device until then.
	ForgetDevice(ctx context.Context, d store.Device) (deferred bool, err error)
}

// NoAdapters is an AdapterRegistry with nothing registered.
type NoAdapters struct{}

func (NoAdapters) ListAdapters(context.Context) []AdapterStatus { return nil }

func (NoAdapters) AdoptDevice(_ context.Context, d store.Device) error {
	return fmt.Errorf("%w: adapter %q is not connected", store.ErrFailedPrecondition, d.Adapter)
}

func (NoAdapters) ForgetDevice(_ context.Context, d store.Device) (bool, error) {
	return false, fmt.Errorf("%w: adapter %q is not connected", store.ErrFailedPrecondition, d.Adapter)
}

// SortedAdapters returns r's adapters ordered by name.
func SortedAdapters(ctx context.Context, r AdapterRegistry) []AdapterStatus {
	out := slices.Clone(r.ListAdapters(ctx))
	slices.SortFunc(out, func(a, b AdapterStatus) int { return strings.Compare(a.Name, b.Name) })
	return out
}
