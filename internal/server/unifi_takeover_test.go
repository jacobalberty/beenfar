package server_test

import (
	"bytes"
	"net"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/jacobalberty/go-unifi/inform"
	"google.golang.org/protobuf/encoding/protojson"

	adminv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1/adminv1connect"
	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
	"github.com/jacobalberty/beenfar/internal/store"
)

// A device managed by another controller shows up locked; once its key is
// imported, its informs decrypt and it appears with its real model; adopting
// it re-keys it.
func TestUnifiTakeover(t *testing.T) {
	f, _ := newUnifiFixture(t)
	ctx := t.Context()
	devices := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))
	admin := adminv1connect.NewAdapterServiceClient(f.client(f.adminToken))
	d := unifiDevice{t: t, f: f, mac: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x20, 0x01}}
	foreign := inform.NewKey()
	foreignHex := inform.FormatKey(foreign)

	// No key opens it yet: it is recorded, locked, and listable.
	if status, _ := d.inform(foreign, "abcdef0123456789"); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	pending, err := devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	if err != nil || len(pending.GetDevices()) != 1 || !pending.GetDevices()[0].GetLocked() || pending.GetDevices()[0].GetModel() != "" {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	id := pending.GetDevices()[0].GetId()
	_, err = devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: id}})
	wantCode(t, err, connect.CodeFailedPrecondition)

	// Importing is for admins only.
	op, err := f.auth.CreateUser(ctx, "operator", "correct horse", false)
	if err != nil {
		t.Fatal(err)
	}
	req := &adminv1.ImportUnifiDeviceKeysRequest{Devices: []*adminv1.UnifiDeviceKey{{Mac: "DE-AD-BE-EF-20-01", Authkey: foreignHex}}}
	_, err = adminv1connect.NewAdapterServiceClient(f.client(f.token(t, op.ID))).ImportUnifiDeviceKeys(ctx, req)
	wantCode(t, err, connect.CodePermissionDenied)
	_, err = adminv1connect.NewAdapterServiceClient(f.client(adapterToken(t, f, "meraki"))).ImportUnifiDeviceKeys(ctx, req)
	wantCode(t, err, connect.CodeUnauthenticated)

	res, err := admin.ImportUnifiDeviceKeys(ctx, req)
	if err != nil || res.GetImported() != 1 {
		t.Fatalf("import = %v, %v", res, err)
	}

	// Its informs decrypt now; it is pending with its real model, unlocked.
	if status, _ := d.inform(foreign, "abcdef0123456789"); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (pending devices wait to be adopted)", status)
	}
	got, err := devices.GetDevice(ctx, &configv1.GetDeviceRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if g := got.GetDevice(); g.GetLocked() || g.GetModel() != "U7PG2" || g.GetFirmware() != "4.0.66.10832" || g.GetState() != configv1.DeviceState_DEVICE_STATE_PENDING {
		t.Fatalf("device = %v", g)
	}

	// Adoption is explicit and re-keys the device, sealing the reply with
	// the key the device used.
	if _, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: id}}); err != nil {
		t.Fatal(err)
	}
	status, reply := d.inform(foreign, "abcdef0123456789")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	newKey, version := authKeyOf(t, reply)
	if bytes.Equal(newKey, foreign) {
		t.Fatal("the device was not re-keyed")
	}
	if status, reply := d.inform(newKey, version); status != http.StatusOK || reply["_type"] != "noop" {
		t.Fatalf("with the new key: %d %v", status, reply)
	}
	if status, _ := d.inform(foreign, "abcdef0123456789"); status != http.StatusNotFound {
		t.Fatalf("imported key after the switch: status = %d, want 404", status)
	}

	// No API ever returns a key.
	list, err := devices.ListDevices(ctx, &configv1.ListDevicesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range [][]byte{foreign, newKey} {
		b, _ := protojson.Marshal(list)
		if strings.Contains(string(b), inform.FormatKey(key)) {
			t.Fatalf("a key is in the device list: %s", b)
		}
	}
	code, body, _ := f.rest(t, http.MethodGet, "/api/v1/devices/"+id, f.adminToken, "")
	if code != http.StatusOK || strings.Contains(string(body), foreignHex) || strings.Contains(string(body), inform.FormatKey(newKey)) {
		t.Fatalf("REST device = %d %s", code, body)
	}
}

func TestImportUnifiDeviceKeysValidation(t *testing.T) {
	f, _ := newUnifiFixture(t)
	ctx := t.Context()
	admin := adminv1connect.NewAdapterServiceClient(f.client(f.adminToken))
	good := inform.FormatKey(inform.NewKey())

	for name, devs := range map[string][]*adminv1.UnifiDeviceKey{
		"bad mac":      {{Mac: "nope", Authkey: good}},
		"short key":    {{Mac: "de:ad:be:ef:20:02", Authkey: "abcd"}},
		"not hex":      {{Mac: "de:ad:be:ef:20:02", Authkey: strings.Repeat("zz", 16)}},
		"one bad of 2": {{Mac: "de:ad:be:ef:20:03", Authkey: good}, {Mac: "de:ad:be:ef:20:04", Authkey: "x"}},
	} {
		_, err := admin.ImportUnifiDeviceKeys(ctx, &adminv1.ImportUnifiDeviceKeysRequest{Devices: devs})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: code = %v (%v), want InvalidArgument", name, connect.CodeOf(err), err)
		} else if strings.Contains(err.Error(), "zz") {
			t.Errorf("%s: the error echoes the key: %v", name, err)
		}
	}
	// Nothing was stored for a rejected request, not even the valid entry.
	list, err := configv1connect.NewDeviceServiceClient(f.client(f.adminToken)).ListDevices(ctx, &configv1.ListDevicesRequest{})
	if err != nil || len(list.GetDevices()) != 0 {
		t.Fatalf("devices after rejected imports = %v, %v", list, err)
	}

	// A device that belongs to another adapter cannot be taken over.
	if _, err := f.store.ReportDevice(ctx, storeDiscovered("de:ad:be:ef:20:05", "meraki")); err != nil {
		t.Fatal(err)
	}
	_, err = admin.ImportUnifiDeviceKeys(ctx, &adminv1.ImportUnifiDeviceKeysRequest{Devices: []*adminv1.UnifiDeviceKey{{Mac: "de:ad:be:ef:20:05", Authkey: good}}})
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func storeDiscovered(mac, adapterName string) store.DiscoveredDevice {
	return store.DiscoveredDevice{MAC: mac, Adapter: adapterName}
}
