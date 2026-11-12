package server_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
	"github.com/jacobalberty/beenfar/internal/store"
)

func TestSwitchPorts(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	ports := configv1connect.NewSwitchPortServiceClient(f.client(f.adminToken))
	nets := configv1connect.NewNetworkServiceClient(f.client(f.adminToken))
	devices := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))

	d, err := f.store.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:20", Adapter: "unifi", Model: "USW-Lite-8-PoE"})
	if err != nil {
		t.Fatal(err)
	}
	newNet := func(name string, vlan uint32) string {
		t.Helper()
		n, err := nets.CreateNetwork(ctx, &configv1.CreateNetworkRequest{Network: &configv1.Network{Name: name, VlanId: vlan}})
		if err != nil {
			t.Fatal(err)
		}
		return n.GetNetwork().GetId()
	}
	lan, iot, cams := newNet("lan", 0), newNet("iot", 20), newNet("cams", 30)

	port := &configv1.SwitchPort{DeviceId: d.ID, PortIndex: 2, NativeNetworkId: lan}

	// Only adopted devices have configurable ports.
	_, err = ports.CreateSwitchPort(ctx, &configv1.CreateSwitchPortRequest{SwitchPort: port})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if _, err := devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: d.ID}}); err != nil {
		t.Fatal(err)
	}
	_, err = ports.CreateSwitchPort(ctx, &configv1.CreateSwitchPortRequest{SwitchPort: &configv1.SwitchPort{DeviceId: "nope", PortIndex: 1}})
	wantCode(t, err, connect.CodeInvalidArgument)

	created, err := ports.CreateSwitchPort(ctx, &configv1.CreateSwitchPortRequest{SwitchPort: port})
	if err != nil {
		t.Fatal(err)
	}
	p := created.GetSwitchPort()
	if p.GetTagging() != configv1.SwitchPortTagging_SWITCH_PORT_TAGGING_ALL || p.GetPoeMode() != configv1.PoeMode_POE_MODE_AUTO {
		t.Fatalf("defaults: %v", p)
	}
	_, err = ports.CreateSwitchPort(ctx, &configv1.CreateSwitchPortRequest{SwitchPort: port})
	wantCode(t, err, connect.CodeAlreadyExists)

	// Terraform manages the VLANs; the UI names the port. Neither undoes
	// the other.
	if _, err := ports.UpdateSwitchPort(ctx, &configv1.UpdateSwitchPortRequest{
		SwitchPort: &configv1.SwitchPort{Id: p.GetId(), Name: "printer"}, UpdateMask: mask("name"),
	}); err != nil {
		t.Fatal(err)
	}
	upd, err := ports.UpdateSwitchPort(ctx, &configv1.UpdateSwitchPortRequest{
		SwitchPort: &configv1.SwitchPort{
			Id: p.GetId(), Tagging: configv1.SwitchPortTagging_SWITCH_PORT_TAGGING_CUSTOM,
			TaggedNetworkIds: []string{iot, cams}, PoeMode: configv1.PoeMode_POE_MODE_OFF,
		},
		UpdateMask: mask("tagging", "tagged_network_ids", "poe_mode"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := upd.GetSwitchPort()
	want := []string{iot, cams}
	slices.Sort(want)
	if got.GetName() != "printer" || got.GetNativeNetworkId() != lan || !slices.Equal(got.GetTaggedNetworkIds(), want) ||
		got.GetPoeMode() != configv1.PoeMode_POE_MODE_OFF {
		t.Fatalf("after updates: %v", got)
	}

	// The port's identity is not updatable.
	for _, bad := range []*configv1.UpdateSwitchPortRequest{
		{SwitchPort: &configv1.SwitchPort{Id: p.GetId(), PortIndex: 5}},
		{SwitchPort: &configv1.SwitchPort{Id: p.GetId(), PortIndex: 5}, UpdateMask: mask("port_index")},
		{SwitchPort: &configv1.SwitchPort{Id: p.GetId(), TaggedNetworkIds: []string{lan}, Tagging: configv1.SwitchPortTagging_SWITCH_PORT_TAGGING_CUSTOM, NativeNetworkId: lan}},
	} {
		_, err := ports.UpdateSwitchPort(ctx, bad)
		wantCode(t, err, connect.CodeInvalidArgument)
	}

	// A network carried by a port cannot be deleted.
	_, err = nets.DeleteNetwork(ctx, &configv1.DeleteNetworkRequest{Id: iot})
	wantCode(t, err, connect.CodeFailedPrecondition)

	code, body, _ := f.rest(t, http.MethodGet, "/api/v1/switch-ports?deviceId="+d.ID, f.adminToken, "")
	if code != http.StatusOK || !strings.Contains(string(body), `"printer"`) || !strings.Contains(string(body), "SWITCH_PORT_TAGGING_CUSTOM") {
		t.Fatalf("REST list = %d %s", code, body)
	}

	// Forgetting the device drops its ports.
	if _, err := devices.ForgetDevice(ctx, &configv1.ForgetDeviceRequest{Id: d.ID}); err != nil {
		t.Fatal(err)
	}
	_, err = ports.GetSwitchPort(ctx, &configv1.GetSwitchPortRequest{Id: p.GetId()})
	wantCode(t, err, connect.CodeNotFound)
}
