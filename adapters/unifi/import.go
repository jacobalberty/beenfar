package unifi

import (
	"context"
	"errors"
	"fmt"

	"github.com/jacobalberty/go-unifi/inform"

	"github.com/jacobalberty/beenfar/api/adapter"
	"github.com/jacobalberty/beenfar/internal/store"
)

// KeyImport is the credential of a device another controller manages, in the
// form outside tools extract it: the shape of one element of the JSON that
// "beenfard admin import-unifi" reads.
type KeyImport struct {
	MAC     string `json:"mac"`
	AuthKey string `json:"authkey"`
	// GCM says whether the device uses AES-GCM. It is informational: every
	// inform says how it is encrypted and the reply uses the same.
	GCM bool `json:"gcm,omitempty"`
}

// ImportKeys stores credentials taken from another controller as imported
// keys, so that informs from those devices decrypt once they are pointed at
// this controller. A device not seen before is recorded as pending, without
// a model until it informs; a locked one is unlocked until it informs again.
// Adoption stays explicit and re-keys the device.
//
// All entries are checked before any is stored. The keys are never returned
// by any API.
func ImportKeys(ctx context.Context, h adapter.Host, in []KeyImport) (int, error) {
	keys := make([][]byte, len(in))
	for i, e := range in {
		mac, err := store.NormalizeMAC(e.MAC)
		if err != nil {
			return 0, fmt.Errorf("%w: entry %d: %v", store.ErrInvalid, i+1, err)
		}
		in[i].MAC = mac
		if keys[i], err = inform.ParseKey(e.AuthKey); err != nil {
			// Say nothing about the value: it is a secret.
			return 0, fmt.Errorf("%w: entry %d (%s): authkey must be 32 hex digits", store.ErrInvalid, i+1, mac)
		}
	}
	for i, e := range in {
		dev, err := h.ReportDevice(ctx, adapter.Discovered{MAC: e.MAC})
		if err != nil {
			if errors.Is(err, adapter.ErrNotFound) {
				return i, fmt.Errorf("%w: device %s belongs to another adapter", store.ErrFailedPrecondition, e.MAC)
			}
			return i, err
		}
		if err := h.SetDeviceSecret(ctx, dev.ID, SecretImported, keys[i]); err != nil {
			return i, err
		}
	}
	return len(in), nil
}
