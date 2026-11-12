package server_test

import (
	"net/http"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/encoding/protojson"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
	"github.com/jacobalberty/beenfar/internal/server"
)

func writerOf(ws []*configv1.FieldWriter, field string) *configv1.FieldWriter {
	for _, w := range ws {
		if w.GetField() == field {
			return w
		}
	}
	return nil
}

// Terraform (an API token) and the UI (a session) update different fields of
// one resource; the resource says who last set which.
func TestFieldWritersTokenAndSession(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken))

	created, err := wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: wpaNetwork("home")})
	if err != nil {
		t.Fatal(err)
	}
	w := created.GetWifiNetwork()
	// The token is named "test" by the fixture; the create names every field.
	if got := writerOf(w.GetFieldWriters(), "ssid"); got.GetWriter() != "test" ||
		got.GetKind() != configv1.FieldWriterKind_FIELD_WRITER_KIND_API_TOKEN || got.GetWriteTime() == nil {
		t.Fatalf("ssid writer after create = %v", got)
	}

	// The UI signs in and sets the AP groups with a session cookie.
	code, body, hdr := f.rest(t, http.MethodPost, "/api/v1/auth/login", "", `{"username":"admin","password":"correct horse"}`)
	if code != http.StatusOK {
		t.Fatalf("login = %d %s", code, body)
	}
	c, err := http.ParseSetCookie(hdr.Get("Set-Cookie"))
	if err != nil || c.Name != server.SessionCookieName {
		t.Fatalf("cookie = %+v, %v", c, err)
	}
	code, body, _ = f.rest(t, http.MethodPatch, "/api/v1/wifi-networks/"+w.GetId()+"?update_mask=apGroups", "",
		`{"apGroups": ["attic"]}`, "Cookie", c.Name+"="+c.Value)
	if code != http.StatusOK {
		t.Fatalf("PATCH as session = %d %s", code, body)
	}
	var updated configv1.WifiNetwork
	if err := protojson.Unmarshal(body, &updated); err != nil {
		t.Fatal(err)
	}
	if got := writerOf(updated.GetFieldWriters(), "ap_groups"); got.GetWriter() != "admin" ||
		got.GetKind() != configv1.FieldWriterKind_FIELD_WRITER_KIND_USER {
		t.Fatalf("ap_groups writer after the UI update = %v", got)
	}
	if got := writerOf(updated.GetFieldWriters(), "ssid"); got.GetWriter() != "test" {
		t.Fatalf("ssid was re-attributed by an update that did not name it: %v", got)
	}

	// The token renames the network; the UI's field keeps its writer.
	if _, err := wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{
		WifiNetwork: &configv1.WifiNetwork{Id: w.GetId(), Ssid: "home2"}, UpdateMask: mask("ssid"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := wifi.GetWifiNetwork(ctx, &configv1.GetWifiNetworkRequest{Id: w.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if writerOf(got.GetWifiNetwork().GetFieldWriters(), "ap_groups").GetWriter() != "admin" ||
		writerOf(got.GetWifiNetwork().GetFieldWriters(), "ssid").GetWriter() != "test" {
		t.Fatalf("writers after Get = %v", got.GetWifiNetwork().GetFieldWriters())
	}
	// The etag still moves only with updates, not with how they are attributed.
	if got.GetWifiNetwork().GetEtag() == w.GetEtag() {
		t.Error("etag did not change after two updates")
	}

	// List carries the writers too.
	list, err := wifi.ListWifiNetworks(ctx, &configv1.ListWifiNetworksRequest{})
	if err != nil || len(list.GetWifiNetworks()) != 1 || len(list.GetWifiNetworks()[0].GetFieldWriters()) == 0 {
		t.Fatalf("list = %v, %v", list, err)
	}

	// Mask typos are still rejected, and nothing is recorded for them.
	_, err = wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{
		WifiNetwork: &configv1.WifiNetwork{Id: w.GetId()}, UpdateMask: mask("sid"),
	})
	wantCode(t, err, connect.CodeInvalidArgument)
	if writerOf(mustWifi(t, wifi, w.GetId()).GetFieldWriters(), "sid") != nil {
		t.Error("a rejected mask path was recorded")
	}

	// Deleting the resource forgets its writers: a new resource starts clean.
	if _, err := wifi.DeleteWifiNetwork(ctx, &configv1.DeleteWifiNetworkRequest{Id: w.GetId()}); err != nil {
		t.Fatal(err)
	}
	again, err := wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: wpaNetwork("home")})
	if err != nil {
		t.Fatal(err)
	}
	if writerOf(again.GetWifiNetwork().GetFieldWriters(), "ap_groups").GetKind() != configv1.FieldWriterKind_FIELD_WRITER_KIND_API_TOKEN {
		t.Errorf("the new network inherited writers: %v", again.GetWifiNetwork().GetFieldWriters())
	}
}

func mustWifi(t *testing.T, c configv1connect.WifiNetworkServiceClient, id string) *configv1.WifiNetwork {
	t.Helper()
	r, err := c.GetWifiNetwork(t.Context(), &configv1.GetWifiNetworkRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	return r.GetWifiNetwork()
}

// The other three resources report writers as well, and the RADIUS shared
// secret's writer shows a rotation without the secret being readable.
func TestFieldWritersOtherResources(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	cl := f.client(f.adminToken)
	networks := configv1connect.NewNetworkServiceClient(cl)
	radius := configv1connect.NewRadiusServerServiceClient(cl)

	n, err := networks.CreateNetwork(ctx, &configv1.CreateNetworkRequest{Network: &configv1.Network{Name: "iot", VlanId: 20}})
	if err != nil {
		t.Fatal(err)
	}
	if writerOf(n.GetNetwork().GetFieldWriters(), "vlan_id").GetWriter() != "test" {
		t.Errorf("network writers = %v", n.GetNetwork().GetFieldWriters())
	}

	r, err := radius.CreateRadiusServer(ctx, &configv1.CreateRadiusServerRequest{
		RadiusServer: &configv1.RadiusServer{Name: "r", Address: "10.0.0.5", SharedSecret: "s3cret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.GetRadiusServer().GetSharedSecret() != "" {
		t.Fatal("the shared secret was returned")
	}
	rotated, err := radius.UpdateRadiusServer(ctx, &configv1.UpdateRadiusServerRequest{
		RadiusServer: &configv1.RadiusServer{Id: r.GetRadiusServer().GetId(), SharedSecret: "new-s3cret"},
		UpdateMask:   mask("shared_secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.GetRadiusServer().GetSharedSecretVersion() != 2 ||
		writerOf(rotated.GetRadiusServer().GetFieldWriters(), "shared_secret") == nil {
		t.Errorf("rotated = %v", rotated.GetRadiusServer())
	}
}
