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

// Adopt brings a pending device under management, or accepts the re-adoption
// of an adopted device that was reset and is awaiting it. Exactly one of id
// and mac must be set.
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
	if d.State != store.DeviceStatePending && d.Readoption != store.ReadoptionAwaiting {
		return store.Device{}, fmt.Errorf("%w: device %s is already adopted", store.ErrFailedPrecondition, d.ID)
	}
	if d.Forgetting {
		return store.Device{}, fmt.Errorf("%w: device %s is being forgotten; it comes back as a new device once it has been reset", store.ErrFailedPrecondition, d.ID)
	}
	if d.Locked {
		return store.Device{}, fmt.Errorf("%w: device %s is locked: its adapter has no credentials for it", store.ErrFailedPrecondition, d.ID)
	}
	if err := s.adapters.AdoptDevice(ctx, d); err != nil {
		return store.Device{}, fmt.Errorf("adapter %q could not adopt device %s: %w", d.Adapter, d.ID, err)
	}
	return s.store.AdoptDevice(ctx, d.ID)
}

// Forget stops managing a device. For an adopted device whose adapter can
// reset it, that means resetting it to its factory state: the adapter does it
// the next time the device makes contact, so the device stays listed, marked
// as forgetting, until the adapter reports it released, and is then dropped
// and comes back as a new, pending device. Asking again while that is pending
// changes nothing. force gives up waiting and removes the device now, without
// resetting it, which leaves the device holding the key beenfar just threw
// away: it then has to be reset by hand. That is how a device that is switched
// off, or never comes back, is removed.
//
// Any other device (pending, locked, or one whose adapter does not defer)
// is removed at once, the adapter being told first so it can release it; if
// the adapter is not connected the device is removed anyway, so a stopped
// adapter cannot make a device undeletable.
//
// It returns the device while it is still being forgotten, and the zero
// device once it has been removed.
func (s *DeviceService) Forget(ctx context.Context, id string, force bool) (store.Device, error) {
	d, err := s.store.GetDevice(ctx, id)
	if err != nil {
		return store.Device{}, err
	}
	if d.Forgetting && !force {
		return d, nil // already waiting for the reset
	}
	toAdapter := d
	if force {
		// A device already marked as forgetting is the adapter's cue to give up
		// waiting and drop what it holds for the device.
		toAdapter.Forgetting = true
	}
	deferred, err := s.adapters.ForgetDevice(ctx, toAdapter)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrFailedPrecondition):
		slog.WarnContext(ctx, "forgetting a device whose adapter is not connected",
			slog.String("adapter", d.Adapter), slog.String("device.id", d.ID))
	default:
		return store.Device{}, fmt.Errorf("adapter %q could not forget device %s: %w", d.Adapter, d.ID, err)
	}
	// Only an adopted device has anything to reset.
	if deferred && !force && d.State == store.DeviceStateAdopted {
		return s.store.MarkDeviceForgetting(ctx, id)
	}
	return store.Device{}, s.store.DeleteDevice(ctx, id)
}
