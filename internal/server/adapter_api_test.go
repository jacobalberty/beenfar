package server_test

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	adapterv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/adapter/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/adapter/v1/adapterv1connect"
	adminv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1/adminv1connect"
	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
)

func adapterToken(t *testing.T, f apiFixture, name string) string {
	t.Helper()
	_, secret, err := f.auth.CreateAdapterToken(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestAdapterAPIAuthentication(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	req := &adapterv1.LookupDeviceRequest{Mac: "de:ad:be:ef:00:01"}

	// No token, an API token and a garbage adapter token are all refused.
	for name, tok := range map[string]string{"none": "", "api token": f.adminToken, "bad": "bfa_aaaaaaaaaaaa_x"} {
		_, err := adapterv1connect.NewAdapterHostServiceClient(f.client(tok)).LookupDevice(ctx, req)
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("%s: code = %v (%v), want Unauthenticated", name, connect.CodeOf(err), err)
		}
	}

	// An adapter token does not open the user API.
	tok := adapterToken(t, f, "meraki")
	_, err := configv1connect.NewDeviceServiceClient(f.client(tok)).ListDevices(ctx, &configv1.ListDevicesRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)

	_, err = adapterv1connect.NewAdapterHostServiceClient(f.client(tok)).LookupDevice(ctx, req)
	wantCode(t, err, connect.CodeNotFound)
}

func TestAdapterAPIHost(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	a := adapterv1connect.NewAdapterHostServiceClient(f.client(adapterToken(t, f, "meraki")))
	other := adapterv1connect.NewAdapterHostServiceClient(f.client(adapterToken(t, f, "other")))

	rep, err := a.ReportDevice(ctx, &adapterv1.ReportDeviceRequest{Discovered: &adapterv1.Discovered{Mac: "DE-AD-BE-EF-00-02", Model: "MR46", Locked: true}})
	if err != nil {
		t.Fatal(err)
	}
	d := rep.GetDevice()
	if d.GetMac() != "de:ad:be:ef:00:02" || d.GetState() != adapterv1.DeviceState_DEVICE_STATE_PENDING || d.GetId() == "" {
		t.Fatalf("device = %v", d)
	}
	look, err := a.LookupDevice(ctx, &adapterv1.LookupDeviceRequest{Mac: "deadbeef0002"})
	if err != nil || look.GetDevice().GetId() != d.GetId() || look.GetDevice().GetLastSeenTime().AsTime().IsZero() ||
		look.GetDevice().GetReadoption() != adapterv1.ReadoptionState_READOPTION_STATE_UNSPECIFIED {
		t.Fatalf("lookup = %v, %v", look, err)
	}
	if _, err := a.ReportState(ctx, &adapterv1.ReportStateRequest{DeviceId: d.GetId(), State: &adapterv1.State{Readoption: adapterv1.ReadoptionState_READOPTION_STATE_AWAITING_ACCEPTANCE}}); err != nil {
		t.Fatal(err)
	}
	if look, err = a.LookupDevice(ctx, &adapterv1.LookupDeviceRequest{Mac: "deadbeef0002"}); err != nil ||
		look.GetDevice().GetReadoption() != adapterv1.ReadoptionState_READOPTION_STATE_AWAITING_ACCEPTANCE {
		t.Fatalf("readoption did not round trip: %v, %v", look, err)
	}

	// The device is listed, locked, through the public API.
	devs := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))
	got, err := devs.GetDevice(ctx, &configv1.GetDeviceRequest{Id: d.GetId()})
	if err != nil || got.GetDevice().GetAdapter() != "meraki" {
		t.Fatalf("device = %v, %v", got, err)
	}

	// Another adapter cannot reach it.
	_, err = other.LookupDevice(ctx, &adapterv1.LookupDeviceRequest{Mac: "de:ad:be:ef:00:02"})
	wantCode(t, err, connect.CodeNotFound)
	_, err = other.GetDeviceSecret(ctx, &adapterv1.GetDeviceSecretRequest{DeviceId: d.GetId(), Name: "k"})
	wantCode(t, err, connect.CodeNotFound)

	// Secrets round trip.
	if s, err := a.GetDeviceSecret(ctx, &adapterv1.GetDeviceSecretRequest{DeviceId: d.GetId(), Name: "k"}); err != nil || s.GetFound() {
		t.Fatalf("missing secret = %v, %v", s, err)
	}
	if _, err := a.SetDeviceSecret(ctx, &adapterv1.SetDeviceSecretRequest{DeviceId: d.GetId(), Name: "k", Secret: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	if s, err := a.GetDeviceSecret(ctx, &adapterv1.GetDeviceSecretRequest{DeviceId: d.GetId(), Name: "k"}); err != nil || !s.GetFound() || string(s.GetSecret()) != "v" {
		t.Fatalf("secret = %v, %v", s, err)
	}

	// State and desired config.
	if _, err := a.ReportState(ctx, &adapterv1.ReportStateRequest{DeviceId: d.GetId(), State: &adapterv1.State{Firmware: "29.1", ConfigRevision: 5, UptimeSeconds: 60}}); err != nil {
		t.Fatal(err)
	}
	if g, _ := devs.GetDevice(ctx, &configv1.GetDeviceRequest{Id: d.GetId()}); g.GetDevice().GetFirmware() != "29.1" {
		t.Fatalf("device = %v", g)
	}
	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken))
	if _, err := wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: wpaNetwork("home")}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.GetDesiredConfig(ctx, &adapterv1.GetDesiredConfigRequest{DeviceId: d.GetId()})
	if err != nil || len(cfg.GetConfig().GetWifi()) != 1 || cfg.GetConfig().GetWifi()[0].GetSsid() != "home" ||
		cfg.GetConfig().GetWifi()[0].GetSecurity() != adapterv1.WifiSecurity_WIFI_SECURITY_WPA_PERSONAL || cfg.GetConfig().GetRevision() <= 0 {
		t.Fatalf("config = %v, %v", cfg, err)
	}
}

func TestAdapterAPIRegisterAndCommands(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	tok := adapterToken(t, f, "meraki")
	a := adapterv1connect.NewAdapterHostServiceClient(f.client(tok))
	admin := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))

	stream, err := a.Register(ctx, &adapterv1.RegisterRequest{Version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	first, err := stream.Receive()
	if err != nil || first.GetRegistered() == nil {
		t.Fatalf("first message = %v, %v", first, err)
	}

	// It is listed as a connected external adapter.
	list, err := adminv1connect.NewAdapterServiceClient(f.client(f.adminToken)).ListAdapters(ctx, &adminv1.ListAdaptersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var found *adminv1.Adapter
	for _, ad := range list.GetAdapters() {
		if ad.GetName() == "meraki" {
			found = ad
		}
	}
	if found == nil || found.GetKind() != adminv1.AdapterKind_ADAPTER_KIND_EXTERNAL || !found.GetHealthy() || found.GetVersion() != "1.2.3" {
		t.Fatalf("adapters = %v", list)
	}

	// A second connection under the same name is refused.
	dup, err := a.Register(ctx, &adapterv1.RegisterRequest{})
	if err == nil {
		_, err = dup.Receive()
		_ = dup.Close()
	}
	wantCode(t, err, connect.CodeAlreadyExists)

	rep, err := a.ReportDevice(ctx, &adapterv1.ReportDeviceRequest{Discovered: &adapterv1.Discovered{Mac: "de:ad:be:ef:00:03"}})
	if err != nil {
		t.Fatal(err)
	}
	id := rep.GetDevice().GetId()

	// Adopt is delivered over the stream; the call returns once the adapter answers.
	type result struct {
		d   *configv1.AdoptDeviceResponse
		err error
	}
	done := make(chan result, 1)
	go func() {
		d, err := admin.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: id}})
		done <- result{d, err}
	}()
	cmd, err := stream.Receive()
	if err != nil || cmd.GetAdopt().GetId() != id || cmd.GetCommandId() == "" {
		t.Fatalf("command = %v, %v", cmd, err)
	}
	// Another adapter cannot answer it.
	stranger := adapterv1connect.NewAdapterHostServiceClient(f.client(adapterToken(t, f, "other")))
	_, err = stranger.CompleteCommand(ctx, &adapterv1.CompleteCommandRequest{CommandId: cmd.GetCommandId()})
	wantCode(t, err, connect.CodeNotFound)
	if _, err := a.CompleteCommand(ctx, &adapterv1.CompleteCommandRequest{CommandId: cmd.GetCommandId()}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.d.GetDevice().GetState() != configv1.DeviceState_DEVICE_STATE_ADOPTED {
			t.Fatalf("adopt = %v, %v", r.d, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("adopt did not return")
	}

	// A failing adapter fails the call.
	go func() {
		_, err := admin.ForgetDevice(ctx, &configv1.ForgetDeviceRequest{Id: id})
		done <- result{nil, err}
	}()
	cmd, err = stream.Receive()
	if err != nil || cmd.GetForget().GetId() != id {
		t.Fatalf("command = %v, %v", cmd, err)
	}
	if _, err := a.CompleteCommand(ctx, &adapterv1.CompleteCommandRequest{CommandId: cmd.GetCommandId(), Error: "device busy"}); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.err == nil {
		t.Fatalf("forget = %v", r.err)
	}
	if _, err := admin.GetDevice(ctx, &configv1.GetDeviceRequest{Id: id}); err != nil {
		t.Fatalf("device must survive a failed forget: %v", err)
	}
}
