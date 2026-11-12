package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jacobalberty/beenfar/internal/store"
)

// SwitchPortService manages the settings of ports on adopted switches.
type SwitchPortService struct {
	store store.Store
}

// Create sets up a port of an adopted device.
func (s *SwitchPortService) Create(ctx context.Context, p store.SwitchPort) (store.SwitchPort, error) {
	p, err := normalizeSwitchPort(p)
	if err != nil {
		return store.SwitchPort{}, err
	}
	var out store.SwitchPort
	err = s.store.WithTx(ctx, func(tx store.Store) error {
		d, err := tx.GetDevice(ctx, p.DeviceID)
		if errors.Is(err, store.ErrNotFound) {
			return invalid("device %q does not exist", p.DeviceID)
		}
		if err != nil {
			return err
		}
		if d.State != store.DeviceStateAdopted {
			return fmt.Errorf("%w: device %s is not adopted; only adopted devices have configurable ports", store.ErrFailedPrecondition, d.ID)
		}
		out, err = tx.CreateSwitchPort(ctx, p)
		if err != nil {
			return err
		}
		return switchPortTracking.afterWrite(ctx, tx, &out, nil)
	})
	return out, err
}

func (s *SwitchPortService) Get(ctx context.Context, id string) (store.SwitchPort, error) {
	p, err := s.store.GetSwitchPort(ctx, id)
	if err != nil {
		return p, err
	}
	return p, switchPortTracking.load(ctx, s.store, &p)
}

// List lists the ports of one device, or of all devices when deviceID is
// empty.
func (s *SwitchPortService) List(ctx context.Context, deviceID string) ([]store.SwitchPort, error) {
	list, err := s.store.ListSwitchPorts(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return list, switchPortTracking.loadAll(ctx, s.store, list)
}

// switchPortFields are the mutable fields of a switch port, by API name.
// device_id and port_index identify the port and are not among them.
var switchPortFields = fieldSetters[store.SwitchPort]{
	"name":               func(d *store.SwitchPort, s store.SwitchPort) { d.Name = s.Name },
	"disabled":           func(d *store.SwitchPort, s store.SwitchPort) { d.Disabled = s.Disabled },
	"native_network_id":  func(d *store.SwitchPort, s store.SwitchPort) { d.NativeNetworkID = s.NativeNetworkID },
	"tagging":            func(d *store.SwitchPort, s store.SwitchPort) { d.Tagging = s.Tagging },
	"tagged_network_ids": func(d *store.SwitchPort, s store.SwitchPort) { d.TaggedNetworkIDs = s.TaggedNetworkIDs },
	"poe_mode":           func(d *store.SwitchPort, s store.SwitchPort) { d.PoEMode = s.PoEMode },
}

// Update replaces the fields named by paths, or every mutable field when
// paths is empty or "*", like WifiService.Update. A device_id or port_index
// that is set must match the stored one.
func (s *SwitchPortService) Update(ctx context.Context, p store.SwitchPort, paths []string) (store.SwitchPort, error) {
	return updateMasked(ctx, s.store, p, paths, switchPortFields,
		func(p *store.SwitchPort) *int64 { return &p.Revision },
		func(ctx context.Context, tx store.Store) (store.SwitchPort, error) {
			cur, err := tx.GetSwitchPort(ctx, p.ID)
			if err != nil {
				return cur, err
			}
			if (p.DeviceID != "" && p.DeviceID != cur.DeviceID) || (p.PortIndex != 0 && p.PortIndex != cur.PortIndex) {
				return cur, invalid("device_id and port_index cannot change; delete the port settings and create new ones")
			}
			return cur, nil
		},
		normalizeSwitchPort,
		func(ctx context.Context, tx store.Store, p store.SwitchPort) (store.SwitchPort, error) {
			return tx.UpdateSwitchPort(ctx, p)
		},
		switchPortTracking.afterWrite)
}

// Delete returns the port to the device's defaults. A non-zero revision
// must match.
func (s *SwitchPortService) Delete(ctx context.Context, id string, revision int64) error {
	return s.store.DeleteSwitchPort(ctx, id, revision)
}

func normalizeSwitchPort(p store.SwitchPort) (store.SwitchPort, error) {
	if p.DeviceID == "" {
		return p, invalid("device_id is required")
	}
	if p.PortIndex < 1 {
		return p, invalid("port_index must be 1 or more")
	}
	if len(p.Name) > 64 {
		return p, invalid("name must be at most 64 bytes")
	}
	if p.Tagging == "" {
		p.Tagging = store.SwitchPortTaggingAll
	}
	if p.PoEMode == "" {
		p.PoEMode = store.PoEModeAuto
	}
	switch p.PoEMode {
	case store.PoEModeAuto, store.PoEModeOff:
	default:
		return p, invalid("unknown poe_mode %q", p.PoEMode)
	}
	switch p.Tagging {
	case store.SwitchPortTaggingAll, store.SwitchPortTaggingNone:
		if len(p.TaggedNetworkIDs) > 0 {
			return p, invalid("tagged_network_ids requires tagging CUSTOM")
		}
	case store.SwitchPortTaggingCustom:
		if p.NativeNetworkID != "" && slices.Contains(p.TaggedNetworkIDs, p.NativeNetworkID) {
			return p, invalid("tagged_network_ids must not include the native network")
		}
	default:
		return p, invalid("unknown tagging %q", p.Tagging)
	}
	if p.TaggedNetworkIDs == nil {
		p.TaggedNetworkIDs = []string{}
	}
	return p, nil
}
