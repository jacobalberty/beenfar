package server_test

import (
	"net/http"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
)

func mask(paths ...string) *fieldmaskpb.FieldMask { return &fieldmaskpb.FieldMask{Paths: paths} }

// A client that manages some fields (Terraform) and one that manages others
// (the UI) can update the same wireless network without undoing each
// other's changes.
func TestUpdateMaskLeavesOtherFieldsAlone(t *testing.T) {
	f := newAPIFixture(t)
	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken))
	ctx := t.Context()

	created, err := wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: wpaNetwork("home")})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetWifiNetwork().GetId()

	// The UI sets the AP associations.
	ui, err := wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{
		WifiNetwork: &configv1.WifiNetwork{Id: id, ApGroups: []string{"garage", "attic"}},
		UpdateMask:  mask("ap_groups"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ui.GetWifiNetwork(); got.GetSsid() != "home" || got.GetSecurityKey() != "hunter2hunter2" ||
		!slices.Equal(got.GetApGroups(), []string{"attic", "garage"}) {
		t.Fatalf("after UI update: %v", got)
	}

	// Terraform renames the network and changes the key; it does not send
	// ap_groups at all.
	tf, err := wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{
		WifiNetwork: &configv1.WifiNetwork{Id: id, Ssid: "home2", SecurityKey: "correct-horse-battery"},
		UpdateMask:  mask("ssid", "security_key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := tf.GetWifiNetwork()
	if got.GetSsid() != "home2" || got.GetSecurityKey() != "correct-horse-battery" ||
		got.GetSecurity() != configv1.WifiSecurity_WIFI_SECURITY_WPA_PERSONAL ||
		!slices.Equal(got.GetApGroups(), []string{"attic", "garage"}) {
		t.Fatalf("after Terraform update: %v", got)
	}

	// A masked update still validates the merged resource: switching to an
	// open network without clearing the stored key is rejected...
	_, err = wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{
		WifiNetwork: &configv1.WifiNetwork{Id: id, Security: configv1.WifiSecurity_WIFI_SECURITY_OPEN},
		UpdateMask:  mask("security"),
	})
	wantCode(t, err, connect.CodeInvalidArgument)

	// ...and unknown or read-only paths are rejected rather than ignored.
	for _, p := range []string{"sid", "id", "etag", "create_time"} {
		_, err = wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{
			WifiNetwork: &configv1.WifiNetwork{Id: id}, UpdateMask: mask(p),
		})
		wantCode(t, err, connect.CodeInvalidArgument)
	}

	// No mask still replaces every field, as before.
	replacement := wpaNetwork("home3")
	replacement.Id = id
	full, err := wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{WifiNetwork: replacement})
	if err != nil {
		t.Fatal(err)
	}
	if got := full.GetWifiNetwork(); got.GetId() != id || !slices.Equal(got.GetApGroups(), []string{"upstairs"}) {
		t.Fatalf("full replace: %v", got)
	}
}

// The etag guards the whole resource, masked or not.
func TestUpdateMaskEtagConflict(t *testing.T) {
	f := newAPIFixture(t)
	nets := configv1connect.NewNetworkServiceClient(f.client(f.adminToken))
	ctx := t.Context()

	created, err := nets.CreateNetwork(ctx, &configv1.CreateNetworkRequest{Network: &configv1.Network{Name: "lan", VlanId: 10}})
	if err != nil {
		t.Fatal(err)
	}
	stale := created.GetNetwork().GetEtag()
	id := created.GetNetwork().GetId()

	// Someone else changes a field this client does not manage.
	if _, err := nets.UpdateNetwork(ctx, &configv1.UpdateNetworkRequest{
		Network: &configv1.Network{Id: id, DomainName: "lan.example"}, UpdateMask: mask("domain_name"),
	}); err != nil {
		t.Fatal(err)
	}

	// With the old etag the update is refused, even though it touches
	// another field...
	_, err = nets.UpdateNetwork(ctx, &configv1.UpdateNetworkRequest{
		Network: &configv1.Network{Id: id, Etag: stale, VlanId: 20}, UpdateMask: mask("vlan_id"),
	})
	wantCode(t, err, connect.CodeAborted)

	// ...and without an etag it applies and keeps the other change.
	upd, err := nets.UpdateNetwork(ctx, &configv1.UpdateNetworkRequest{
		Network: &configv1.Network{Id: id, VlanId: 20}, UpdateMask: mask("vlan_id"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := upd.GetNetwork(); got.GetVlanId() != 20 || got.GetDomainName() != "lan.example" || got.GetName() != "lan" {
		t.Fatalf("got %v", got)
	}

	_, err = nets.UpdateNetwork(ctx, &configv1.UpdateNetworkRequest{
		Network: &configv1.Network{Id: "nope", Name: "x"}, UpdateMask: mask("name"),
	})
	wantCode(t, err, connect.CodeNotFound)
}

// REST: PATCH with ?update_mask= is the same call.
func TestUpdateMaskREST(t *testing.T) {
	f := newAPIFixture(t)
	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken))
	created, err := wifi.CreateWifiNetwork(t.Context(), &configv1.CreateWifiNetworkRequest{WifiNetwork: wpaNetwork("home")})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetWifiNetwork().GetId()

	code, body, _ := f.rest(t, http.MethodPatch, "/api/v1/wifi-networks/"+id+"?update_mask=hidden,apGroups", f.adminToken,
		`{"hidden": true, "apGroups": ["porch"]}`)
	if code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", code, body)
	}
	var got configv1.WifiNetwork
	if err := protojson.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.GetHidden() || !slices.Equal(got.GetApGroups(), []string{"porch"}) || got.GetSsid() != "home" ||
		got.GetSecurityKey() != "hunter2hunter2" {
		t.Fatalf("PATCH result: %v", &got)
	}

	code, body, _ = f.rest(t, http.MethodPatch, "/api/v1/wifi-networks/"+id+"?update_mask=bogus", f.adminToken, `{}`)
	if code != http.StatusBadRequest {
		t.Fatalf("PATCH bogus mask = %d %s, want 400", code, body)
	}
}
