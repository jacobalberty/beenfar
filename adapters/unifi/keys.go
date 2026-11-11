package unifi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jacobalberty/go-unifi/inform"

	"github.com/jacobalberty/beenfar/api/adapter"
)

// The names under which a device's AES keys are stored as adapter secrets.
const (
	// SecretCurrent is the key the device is known to use.
	SecretCurrent = "current"
	// SecretPending is a new key the device was told to switch to. It
	// becomes the current key once the device uses it.
	SecretPending = "pending"
	// SecretImported is a key taken over from another controller.
	SecretImported = "imported"
)

// keyOrder is the order in which a device's keys are tried. A pending key is
// tried first: once the device has switched to it, it is the likeliest.
var keyOrder = [...]string{SecretPending, SecretCurrent, SecretImported}

// keyDefault names inform.DefaultKey in Handler.Keys and Serve.
const keyDefault = "default"

// keysFor returns a device's candidate keys: pending, current, imported, then
// the default key that unadopted devices use. Unknown devices only get the
// default key.
//
// Once a device has a current key, the default key is not offered while the
// device is being heard from: it is the key anybody on the network can seal a
// packet with, so it must not open a device that has a private one. It is
// offered once the device has been silent for readoptAfter (0 never offers
// it), because a device that comes back after that long may have been factory
// reset and can only speak the default key; and while the device is being
// readopted. Opening an inform is harmless; what is sent in reply is decided
// by respond, and a reset device is sent nothing until its re-adoption has
// been accepted.
func keysFor(ctx context.Context, h adapter.Host, mac string, readoptAfter time.Duration, now time.Time) ([]inform.Key, error) {
	var keys []inform.Key
	hasCurrent, mayBeReset := false, false
	dev, err := h.DeviceByMAC(ctx, mac)
	switch {
	case errors.Is(err, adapter.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		for _, name := range keyOrder {
			b, err := h.DeviceSecret(ctx, dev.ID, name)
			if errors.Is(err, adapter.ErrNoSecret) {
				continue
			}
			if err != nil {
				return nil, err
			}
			keys = append(keys, inform.Key{Name: name, Bytes: b})
			hasCurrent = hasCurrent || name == SecretCurrent
		}
		mayBeReset = readoptAfter > 0 && dev.State == adapter.DeviceAdopted &&
			(dev.Readoption != adapter.ReadoptionNone || now.Sub(dev.LastSeen) > readoptAfter)
	}
	if hasCurrent && !mayBeReset {
		return keys, nil
	}
	return append(keys, inform.Key{Name: keyDefault, Bytes: inform.DefaultKey()}), nil
}

// commitKey makes the pending key the device's current one, after the device
// has used it: the old key and any imported one are deleted. The new key is
// written first, so a failure part way leaves the device with a working key.
func commitKey(ctx context.Context, h adapter.Host, deviceID string, pending []byte) error {
	if err := h.SetDeviceSecret(ctx, deviceID, SecretCurrent, pending); err != nil {
		return fmt.Errorf("committing key: %w", err)
	}
	for _, name := range []string{SecretPending, SecretImported} {
		if err := h.SetDeviceSecret(ctx, deviceID, name, nil); err != nil {
			return fmt.Errorf("committing key: %w", err)
		}
	}
	return nil
}

// formatRevision renders a config revision as a cfgversion: 16 hex digits,
// like the ones real controllers send.
func formatRevision(rev int64) string { return fmt.Sprintf("%016x", uint64(rev)) }

// parseRevision reads a cfgversion back. Versions this controller did not
// send (devices report their own before they are adopted) read as 0.
func parseRevision(s string) int64 {
	n, err := strconv.ParseUint(s, 16, 63)
	if err != nil || len(s) != 16 {
		return 0
	}
	return int64(n)
}
