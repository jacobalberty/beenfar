package core

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/jacobalberty/beenfar/internal/store"
)

// SettingsSchema describes the settings an operator can change on devices of one
// model: the baseline each starts from, and a check for a set of them. Their
// names and meaning belong to the adapter.
type SettingsSchema struct {
	// Defaults are the baseline settings of a device of the model.
	Defaults map[string]string
	// Check reports whether every key is a setting of the model and every
	// value acceptable for it.
	Check func(map[string]string) error
}

// SettingsSchemas is implemented by an AdapterRegistry whose adapters have
// settings. A registry without it, or an adapter without a schema for the
// model, has none: the device has no settings to change.
type SettingsSchemas interface {
	// SettingsSchema returns the schema of a model handled by an adapter.
	SettingsSchema(adapter, model string) (SettingsSchema, bool)
}

func (s *DeviceService) schema(d store.Device) (SettingsSchema, bool) {
	ss, ok := s.adapters.(SettingsSchemas)
	if !ok {
		return SettingsSchema{}, false
	}
	return ss.SettingsSchema(d.Adapter, d.Model)
}

// Settings returns a device's original and current settings. A device whose
// model has no settings has none (both empty); one whose settings have not been
// stored yet (adopted before they existed) shows the baseline for both.
func (s *DeviceService) Settings(ctx context.Context, id string) (store.DeviceSettings, error) {
	d, err := s.store.GetDevice(ctx, id)
	if err != nil {
		return store.DeviceSettings{}, err
	}
	sc, ok := s.schema(d)
	if !ok {
		return store.DeviceSettings{Original: map[string]string{}, Current: map[string]string{}}, nil
	}
	got, err := s.store.GetDeviceSettings(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return baseline(sc), nil
	}
	return got, err
}

// UpdateSettings changes the current settings of an adopted device: the keys of
// set are replaced and the others are kept. A key that is not a setting of the
// device's model, or a value it does not accept, is invalid and nothing is
// changed.
func (s *DeviceService) UpdateSettings(ctx context.Context, id string, set map[string]string) (store.DeviceSettings, error) {
	if len(set) == 0 {
		return store.DeviceSettings{}, invalid("no settings to change")
	}
	var out store.DeviceSettings
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		d, sc, err := s.adoptedWithSchema(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := sc.Check(set); err != nil {
			return invalid("%v", err)
		}
		out, err = storedOrBaseline(ctx, tx, id, sc)
		if err != nil {
			return err
		}
		out.Current = maps.Clone(out.Current)
		maps.Copy(out.Current, set)
		if err := sc.Check(out.Current); err != nil {
			return invalid("%v", err)
		}
		return tx.SetDeviceSettings(ctx, d.ID, out)
	})
	return out, err
}

// ResetSettings returns a device's current settings to its original ones.
func (s *DeviceService) ResetSettings(ctx context.Context, id string) (store.DeviceSettings, error) {
	var out store.DeviceSettings
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		d, sc, err := s.adoptedWithSchema(ctx, tx, id)
		if err != nil {
			return err
		}
		out, err = storedOrBaseline(ctx, tx, id, sc)
		if err != nil {
			return err
		}
		out.Current = maps.Clone(out.Original)
		return tx.SetDeviceSettings(ctx, d.ID, out)
	})
	return out, err
}

func (s *DeviceService) adoptedWithSchema(ctx context.Context, tx store.Store, id string) (store.Device, SettingsSchema, error) {
	d, err := tx.GetDevice(ctx, id)
	if err != nil {
		return d, SettingsSchema{}, err
	}
	sc, ok := s.schema(d)
	if !ok {
		return d, sc, fmt.Errorf("%w: device %s (model %q) has no settings to change", store.ErrFailedPrecondition, d.ID, d.Model)
	}
	if d.State != store.DeviceStateAdopted {
		return d, sc, fmt.Errorf("%w: device %s is not adopted; its settings are created when it is", store.ErrFailedPrecondition, d.ID)
	}
	return d, sc, nil
}

// ensureSettings stores the baseline of a device's settings the first time,
// when it is adopted: that is the "original" a reset returns to.
func (s *DeviceService) ensureSettings(ctx context.Context, d store.Device) error {
	sc, ok := s.schema(d)
	if !ok {
		return nil
	}
	return s.store.WithTx(ctx, func(tx store.Store) error {
		if _, err := tx.GetDeviceSettings(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
			return err
		}
		return tx.SetDeviceSettings(ctx, d.ID, baseline(sc))
	})
}

func storedOrBaseline(ctx context.Context, tx store.Store, id string, sc SettingsSchema) (store.DeviceSettings, error) {
	got, err := tx.GetDeviceSettings(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return baseline(sc), nil
	}
	return got, err
}

func baseline(sc SettingsSchema) store.DeviceSettings {
	return store.DeviceSettings{Original: maps.Clone(sc.Defaults), Current: maps.Clone(sc.Defaults)}
}
