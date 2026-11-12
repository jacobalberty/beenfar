package server_test

import (
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
)

func TestRadiusServers(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	radius := configv1connect.NewRadiusServerServiceClient(f.client(f.adminToken))
	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken))

	_, err := radius.CreateRadiusServer(ctx, &configv1.CreateRadiusServerRequest{RadiusServer: &configv1.RadiusServer{
		Name: "corp", Address: "10.0.0.2",
	}})
	wantCode(t, err, connect.CodeInvalidArgument) // no secret
	_, err = radius.CreateRadiusServer(ctx, &configv1.CreateRadiusServerRequest{RadiusServer: &configv1.RadiusServer{
		Name: "corp", Address: "radius.example", SharedSecret: "s3cret",
	}})
	wantCode(t, err, connect.CodeInvalidArgument) // not an IP address

	created, err := radius.CreateRadiusServer(ctx, &configv1.CreateRadiusServerRequest{RadiusServer: &configv1.RadiusServer{
		Name: "corp", Address: "10.0.0.2", AccountingPort: 1813, SharedSecret: "s3cret",
	}})
	if err != nil {
		t.Fatal(err)
	}
	r := created.GetRadiusServer()
	if r.GetAuthPort() != 1812 || r.GetAccountingPort() != 1813 || r.GetSharedSecretVersion() != 1 || r.GetSharedSecret() != "" {
		t.Fatalf("created = %v", r)
	}

	// Sending back what Get returned (no secret) keeps the secret.
	got, err := radius.GetRadiusServer(ctx, &configv1.GetRadiusServerRequest{Id: r.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetRadiusServer().GetSharedSecret() != "" {
		t.Fatal("Get returned the shared secret")
	}
	back := got.GetRadiusServer()
	back.Name = "corp-1"
	upd, err := radius.UpdateRadiusServer(ctx, &configv1.UpdateRadiusServerRequest{RadiusServer: back})
	if err != nil {
		t.Fatal(err)
	}
	if v := upd.GetRadiusServer().GetSharedSecretVersion(); v != 1 {
		t.Fatalf("secret version after update without secret = %d, want 1", v)
	}
	upd, err = radius.UpdateRadiusServer(ctx, &configv1.UpdateRadiusServerRequest{
		RadiusServer: &configv1.RadiusServer{Id: r.GetId(), SharedSecret: "n3w-secret"},
		UpdateMask:   mask("shared_secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := upd.GetRadiusServer(); got.GetSharedSecretVersion() != 2 || got.GetName() != "corp-1" || got.GetSharedSecret() != "" {
		t.Fatalf("after secret change: %v", got)
	}

	// WPA enterprise refers to the server; other modes may not.
	_, err = wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: &configv1.WifiNetwork{
		Ssid: "corp", Security: configv1.WifiSecurity_WIFI_SECURITY_WPA_ENTERPRISE,
	}})
	wantCode(t, err, connect.CodeInvalidArgument)
	psk := wpaNetwork("psk")
	psk.RadiusServerId = r.GetId()
	_, err = wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: psk})
	wantCode(t, err, connect.CodeInvalidArgument)
	w, err := wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: &configv1.WifiNetwork{
		Ssid: "corp", Security: configv1.WifiSecurity_WIFI_SECURITY_WPA_ENTERPRISE, RadiusServerId: r.GetId(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if w.GetWifiNetwork().GetRadiusServerId() != r.GetId() {
		t.Fatalf("wifi = %v", w.GetWifiNetwork())
	}

	_, err = radius.DeleteRadiusServer(ctx, &configv1.DeleteRadiusServerRequest{Id: r.GetId()})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if _, err := wifi.DeleteWifiNetwork(ctx, &configv1.DeleteWifiNetworkRequest{Id: w.GetWifiNetwork().GetId()}); err != nil {
		t.Fatal(err)
	}
	_, err = radius.DeleteRadiusServer(ctx, &configv1.DeleteRadiusServerRequest{Id: r.GetId(), Etag: r.GetEtag()})
	wantCode(t, err, connect.CodeAborted)
	if _, err := radius.DeleteRadiusServer(ctx, &configv1.DeleteRadiusServerRequest{Id: r.GetId()}); err != nil {
		t.Fatal(err)
	}
}

// The secret stays out of REST responses too.
func TestRadiusServerREST(t *testing.T) {
	f := newAPIFixture(t)
	code, body, _ := f.rest(t, http.MethodPost, "/api/v1/radius-servers", f.adminToken,
		`{"name": "corp", "address": "2001:db8::2", "sharedSecret": "s3cret"}`)
	if code != http.StatusOK {
		t.Fatalf("POST = %d %s", code, body)
	}
	if strings.Contains(string(body), "s3cret") || !strings.Contains(string(body), `"sharedSecretVersion":"1"`) {
		t.Fatalf("POST body = %s", body)
	}
	code, body, _ = f.rest(t, http.MethodGet, "/api/v1/radius-servers", f.adminToken, "")
	if code != http.StatusOK || strings.Contains(string(body), "s3cret") || !strings.Contains(string(body), `"2001:db8::2"`) {
		t.Fatalf("GET = %d %s", code, body)
	}
}
