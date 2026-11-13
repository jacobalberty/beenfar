package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jacobalberty/beenfar/internal/store"
)

// settingsRegistry adopts anything and has settings for model "AP-1".
type settingsRegistry struct{ NoAdapters }

func (settingsRegistry) AdoptDevice(context.Context, store.Device) error { return nil }

func (settingsRegistry) SettingsSchema(adapter, model string) (SettingsSchema, bool) {
	if adapter != "test" || model != "AP-1" {
		return SettingsSchema{}, false
	}
	return SettingsSchema{
		Defaults: map[string]string{"channel": "auto", "power": "auto"},
		Check: func(set map[string]string) error {
			for k, v := range set {
				switch k {
				case "channel", "power":
					if v == "" || strings.ContainsAny(v, " \n") {
						return fmt.Errorf("%s: %q is not acceptable", k, v)
					}
				default:
					return fmt.Errorf("%q is not a setting of AP-1", k)
				}
			}
			return nil
		},
	}, true
}

func settingsDevice(t *testing.T, svc *Services, st store.Store, mac, model string) store.Device {
	t.Helper()
	d, err := st.ReportDevice(t.Context(), store.DiscoveredDevice{MAC: mac, Adapter: "test", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDeviceSettingsLifecycle(t *testing.T) {
	ctx := t.Context()
	st := openCoreStore(t)
	svc := New(st, settingsRegistry{})
	d := settingsDevice(t, svc, st, "de:ad:be:ef:01:01", "AP-1")

	// Pending: nothing stored yet, and nothing can be changed.
	if _, err := svc.Devices.UpdateSettings(ctx, d.ID, map[string]string{"channel": "6"}); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("update on a pending device: %v", err)
	}

	// Adopting stores the baseline as both original and current.
	if _, err := svc.Devices.Adopt(ctx, d.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Devices.Settings(ctx, d.ID)
	if err != nil || got.Original["channel"] != "auto" || got.Current["power"] != "auto" {
		t.Fatalf("after adoption: %+v, %v", got, err)
	}

	// Setting changes current only, and keeps the other keys.
	got, err = svc.Devices.UpdateSettings(ctx, d.ID, map[string]string{"channel": "6"})
	if err != nil || got.Current["channel"] != "6" || got.Current["power"] != "auto" || got.Original["channel"] != "auto" {
		t.Fatalf("after update: %+v, %v", got, err)
	}
	if stored, _ := st.GetDeviceSettings(ctx, d.ID); stored.Current["channel"] != "6" {
		t.Fatalf("not stored: %+v", stored)
	}

	// A bad key or value is refused and changes nothing.
	for name, set := range map[string]map[string]string{
		"unknown key": {"cwm": "1"}, "bad value": {"channel": "a b"}, "nothing": {},
	} {
		if _, err := svc.Devices.UpdateSettings(ctx, d.ID, set); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if got, _ = svc.Devices.Settings(ctx, d.ID); got.Current["channel"] != "6" {
		t.Errorf("a refused update changed the settings: %+v", got)
	}

	// Reset returns current to original, leaving original alone.
	got, err = svc.Devices.ResetSettings(ctx, d.ID)
	if err != nil || got.Current["channel"] != "auto" || got.Original["channel"] != "auto" {
		t.Fatalf("after reset: %+v, %v", got, err)
	}
}

// A device whose model has no settings shows none and cannot be given any.
func TestDeviceWithoutSettings(t *testing.T) {
	ctx := t.Context()
	st := openCoreStore(t)
	svc := New(st, settingsRegistry{})
	d := settingsDevice(t, svc, st, "de:ad:be:ef:01:02", "OTHER")
	if _, err := svc.Devices.Adopt(ctx, d.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Devices.Settings(ctx, d.ID)
	if err != nil || len(got.Original) != 0 || len(got.Current) != 0 {
		t.Fatalf("settings of a model with none: %+v, %v", got, err)
	}
	if _, err := st.GetDeviceSettings(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("settings were stored for a model with none: %v", err)
	}
	if _, err := svc.Devices.UpdateSettings(ctx, d.ID, map[string]string{"channel": "6"}); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Errorf("update: %v", err)
	}
	if _, err := svc.Devices.ResetSettings(ctx, d.ID); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Errorf("reset: %v", err)
	}
	if _, err := svc.Devices.Settings(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
}

// A device adopted before settings existed shows the baseline, and storing
// happens on its first change, with the original still the baseline.
func TestSettingsOfADeviceAdoptedEarlier(t *testing.T) {
	ctx := t.Context()
	st := openCoreStore(t)
	svc := New(st, settingsRegistry{})
	d := settingsDevice(t, svc, st, "de:ad:be:ef:01:03", "AP-1")
	if _, err := st.AdoptDevice(ctx, d.ID); err != nil { // adopted without the service
		t.Fatal(err)
	}
	got, err := svc.Devices.Settings(ctx, d.ID)
	if err != nil || got.Current["channel"] != "auto" {
		t.Fatalf("baseline: %+v, %v", got, err)
	}
	got, err = svc.Devices.UpdateSettings(ctx, d.ID, map[string]string{"power": "20"})
	if err != nil || got.Original["power"] != "auto" || got.Current["power"] != "20" {
		t.Fatalf("update: %+v, %v", got, err)
	}
}

// Adopting again (a re-adoption) keeps the operator's settings and the original.
func TestReadoptionKeepsSettings(t *testing.T) {
	ctx := t.Context()
	st := openCoreStore(t)
	svc := New(st, settingsRegistry{})
	d := settingsDevice(t, svc, st, "de:ad:be:ef:01:04", "AP-1")
	if _, err := svc.Devices.Adopt(ctx, d.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Devices.UpdateSettings(ctx, d.ID, map[string]string{"channel": "11"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Devices.ensureSettings(ctx, d); err != nil { // what a second adoption runs
		t.Fatal(err)
	}
	if got, _ := svc.Devices.Settings(ctx, d.ID); got.Current["channel"] != "11" {
		t.Errorf("settings were overwritten: %+v", got)
	}
}
