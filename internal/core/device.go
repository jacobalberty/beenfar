package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jacobalberty/beenfar/internal/store"
)

// DeviceService manages adoption of discovered devices.
type DeviceService struct {
	store    store.Store
	adapters AdapterRegistry
}

func (s *DeviceService) Get(ctx context.Context, id string) (store.Device, error) {
	return s.store.GetDevice(ctx, id)
}

func (s *DeviceService) List(ctx context.Context, f store.DeviceFilter) ([]store.Device, error) {
	return s.store.ListDevices(ctx, f)
}

// ListPending lists devices that have been seen but not adopted.
func (s *DeviceService) ListPending(ctx context.Context, adapter string) ([]store.Device, error) {
	return s.store.ListDevices(ctx, store.DeviceFilter{State: store.DeviceStatePending, Adapter: adapter})
}

// Adopt brings a pending device under management. Exactly one of id and mac
// must be set.
//
// The device's adapter takes the device over first; only when that succeeds
// is the device marked adopted. The adapter is not called inside a
// transaction: it may call back into the store while it works.
func (s *DeviceService) Adopt(ctx context.Context, id, mac string) (store.Device, error) {
	if (id == "") == (mac == "") {
		return store.Device{}, invalid("exactly one of id and mac is required")
	}
	var (
		d   store.Device
		err error
	)
	if mac != "" {
		d, err = s.store.GetDeviceByMAC(ctx, mac)
	} else {
		d, err = s.store.GetDevice(ctx, id)
	}
	if err != nil {
		return store.Device{}, err
	}
	if d.State != store.DeviceStatePending {
		return store.Device{}, fmt.Errorf("%w: device %s is already adopted", store.ErrFailedPrecondition, d.ID)
	}
	if d.Locked {
		return store.Device{}, fmt.Errorf("%w: device %s is locked: its adapter has no credentials for it", store.ErrFailedPrecondition, d.ID)
	}
	if err := s.adapters.AdoptDevice(ctx, d); err != nil {
		return store.Device{}, fmt.Errorf("adapter %q could not adopt device %s: %w", d.Adapter, d.ID, err)
	}
	return s.store.AdoptDevice(ctx, d.ID)
}

// Forget removes a device in any state. Its adapter is told first, so it can
// release the device; if the adapter is not connected the device is removed
// anyway, so a stopped adapter cannot make a device undeletable.
func (s *DeviceService) Forget(ctx context.Context, id string) error {
	d, err := s.store.GetDevice(ctx, id)
	if err != nil {
		return err
	}
	if err := s.adapters.ForgetDevice(ctx, d); err != nil {
		if !errors.Is(err, store.ErrFailedPrecondition) {
			return fmt.Errorf("adapter %q could not forget device %s: %w", d.Adapter, d.ID, err)
		}
		slog.WarnContext(ctx, "forgetting a device whose adapter is not connected",
			slog.String("adapter", d.Adapter), slog.String("device.id", d.ID))
	}
	return s.store.DeleteDevice(ctx, id)
}
