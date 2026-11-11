package core

import (
	"context"

	"github.com/jacobalberty/beenfar/internal/store"
)

// DeviceService manages adoption of discovered devices.
type DeviceService struct {
	store store.Store
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
// TODO(phase 4): hand the device to its adapter (Adapter.Adopt) in the same
// operation.
func (s *DeviceService) Adopt(ctx context.Context, id, mac string) (store.Device, error) {
	if (id == "") == (mac == "") {
		return store.Device{}, invalid("exactly one of id and mac is required")
	}
	var out store.Device
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		if mac != "" {
			d, err := tx.GetDeviceByMAC(ctx, mac)
			if err != nil {
				return err
			}
			id = d.ID
		}
		var err error
		out, err = tx.AdoptDevice(ctx, id)
		return err
	})
	return out, err
}

// Forget removes a device in any state.
//
// TODO(phase 4): tell the device's adapter (Adapter.Forget).
func (s *DeviceService) Forget(ctx context.Context, id string) error {
	return s.store.DeleteDevice(ctx, id)
}
