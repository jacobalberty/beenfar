// Package adapterhost connects device adapters to the controller.
//
// It provides the Host that adapters call (in process for built-in adapters,
// behind the adapter API for external ones) and the Registry of connected
// adapters, which core uses to hand devices to their adapter and the admin
// API uses to list them.
package adapterhost

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/jacobalberty/beenfar/api/adapter"
	"github.com/jacobalberty/beenfar/internal/store"
)

// Host implements adapter.Host for one adapter, on top of the store. It only
// lets an adapter see and change the devices it reported itself.
type Host struct {
	name  string
	store store.Store
}

var _ adapter.Host = (*Host)(nil)

// NewHost returns the Host for the adapter called name.
func NewHost(name string, st store.Store) *Host { return &Host{name: name, store: st} }

func (h *Host) ReportDevice(ctx context.Context, d adapter.Discovered) (adapter.Device, error) {
	// ReportDevice would hand a known MAC over to the reporting adapter, so
	// refuse before it can.
	if mac, err := store.NormalizeMAC(d.MAC); err == nil {
		if existing, err := h.store.GetDeviceByMAC(ctx, mac); err == nil && existing.Adapter != h.name {
			return adapter.Device{}, fmt.Errorf("%w: device %s belongs to another adapter", adapter.ErrNotFound, mac)
		}
	}
	dev, err := h.store.ReportDevice(ctx, store.DiscoveredDevice{
		MAC:      d.MAC,
		Adapter:  h.name,
		Model:    d.Model,
		IP:       d.IP,
		Firmware: d.Firmware,
		Locked:   d.Locked,
	})
	if err != nil {
		return adapter.Device{}, hostErr(err)
	}
	return toDevice(dev), nil
}

func (h *Host) DeviceByMAC(ctx context.Context, mac string) (adapter.Device, error) {
	dev, err := h.store.GetDeviceByMAC(ctx, mac)
	if err != nil {
		return adapter.Device{}, hostErr(err)
	}
	if dev.Adapter != h.name {
		return adapter.Device{}, adapter.ErrNotFound
	}
	return toDevice(dev), nil
}

func (h *Host) DesiredConfig(ctx context.Context, deviceID string) (adapter.DesiredConfig, error) {
	if _, err := h.device(ctx, deviceID); err != nil {
		return adapter.DesiredConfig{}, err
	}
	var cfg adapter.DesiredConfig
	err := h.store.WithTx(ctx, func(tx store.Store) error {
		networks, err := tx.ListNetworks(ctx)
		if err != nil {
			return err
		}
		wifi, err := tx.ListWifiNetworks(ctx)
		if err != nil {
			return err
		}
		cfg = buildConfig(networks, wifi)
		return nil
	})
	if err != nil {
		return adapter.DesiredConfig{}, hostErr(err)
	}
	return cfg, nil
}

func (h *Host) ReportState(ctx context.Context, deviceID string, s adapter.State) error {
	if _, err := h.device(ctx, deviceID); err != nil {
		return err
	}
	_, err := h.store.RecordDeviceState(ctx, deviceID, store.DeviceReport{
		Model:          s.Model,
		IP:             s.IP,
		Firmware:       s.Firmware,
		ConfigRevision: s.ConfigRevision,
		Readoption:     store.Readoption(s.Readoption),
	})
	return hostErr(err)
}

func (h *Host) DeviceSecret(ctx context.Context, deviceID, name string) ([]byte, error) {
	if _, err := h.device(ctx, deviceID); err != nil {
		return nil, err
	}
	secret, err := h.store.GetDeviceSecret(ctx, deviceID, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, adapter.ErrNoSecret
	}
	return secret, hostErr(err)
}

// SetDeviceSecret stores the secret, or removes it when secret is empty.
func (h *Host) SetDeviceSecret(ctx context.Context, deviceID, name string, secret []byte) error {
	if _, err := h.device(ctx, deviceID); err != nil {
		return err
	}
	if len(secret) == 0 {
		return hostErr(h.store.DeleteDeviceSecret(ctx, deviceID, name))
	}
	return hostErr(h.store.SetDeviceSecret(ctx, deviceID, name, secret))
}

// device loads a device and checks that it belongs to this adapter.
func (h *Host) device(ctx context.Context, id string) (store.Device, error) {
	dev, err := h.store.GetDevice(ctx, id)
	if err != nil {
		return store.Device{}, hostErr(err)
	}
	if dev.Adapter != h.name {
		return store.Device{}, adapter.ErrNotFound
	}
	return dev, nil
}

func hostErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: %v", adapter.ErrNotFound, err)
	}
	return err
}

func toDevice(d store.Device) adapter.Device {
	return adapter.Device{
		ID:             d.ID,
		MAC:            d.MAC,
		State:          adapter.DeviceState(d.State),
		ConfigRevision: d.ConfigRevision,
		LastSeen:       d.LastSeen,
		Readoption:     adapter.Readoption(d.Readoption),
	}
}

func buildConfig(networks []store.Network, wifi []store.WifiNetwork) adapter.DesiredConfig {
	// The revision is a digest of every resource's id and revision, so it
	// changes when anything is added, removed or updated.
	h := fnv.New64a()
	put := func(kind byte, id string, rev int64) {
		h.Write([]byte{kind})
		h.Write([]byte(id))
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(rev))
		h.Write(b[:])
	}
	cfg := adapter.DesiredConfig{
		Networks: make([]adapter.Network, 0, len(networks)),
		Wifi:     make([]adapter.WifiNetwork, 0, len(wifi)),
	}
	for _, n := range networks {
		put('n', n.ID, n.Revision)
		cfg.Networks = append(cfg.Networks, adapter.Network{
			ID: n.ID, Name: n.Name, VLANID: n.VLANID, Subnet: n.Subnet,
			DHCPEnabled: n.DHCPEnabled, DHCPStart: n.DHCPStart, DHCPStop: n.DHCPStop,
			DomainName: n.DomainName,
		})
	}
	for _, w := range wifi {
		put('w', w.ID, w.Revision)
		cfg.Wifi = append(cfg.Wifi, adapter.WifiNetwork{
			ID: w.ID, SSID: w.SSID, Security: adapter.WifiSecurity(w.Security),
			SecurityKey: w.SecurityKey, Band: adapter.WifiBand(w.Band),
			NetworkID: w.NetworkID, Guest: w.Guest, Hidden: w.Hidden,
		})
	}
	// Clear the sign bit so the revision stays a positive number that
	// fits an int64 column and a decimal cfgversion.
	cfg.Revision = int64(h.Sum64() >> 1)
	return cfg
}
