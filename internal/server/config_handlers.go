package server

import (
	"context"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
	"github.com/jacobalberty/beenfar/internal/apiconv"
	apiconvgen "github.com/jacobalberty/beenfar/internal/apiconv/gen"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
)

// --- WifiNetworkService ---

type wifiHandler struct {
	configv1connect.UnimplementedWifiNetworkServiceHandler
	svc *core.WifiService
}

func (h *wifiHandler) CreateWifiNetwork(ctx context.Context, req *configv1.CreateWifiNetworkRequest) (*configv1.CreateWifiNetworkResponse, error) {
	w, err := apiconvgen.WifiFromProto(req.GetWifiNetwork())
	if err != nil {
		return nil, err
	}
	w.Revision = 0
	out, err := h.svc.Create(ctx, w)
	if err != nil {
		return nil, err
	}
	return &configv1.CreateWifiNetworkResponse{WifiNetwork: apiconvgen.WifiToProto(out)}, nil
}

func (h *wifiHandler) GetWifiNetwork(ctx context.Context, req *configv1.GetWifiNetworkRequest) (*configv1.GetWifiNetworkResponse, error) {
	out, err := h.svc.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &configv1.GetWifiNetworkResponse{WifiNetwork: apiconvgen.WifiToProto(out)}, nil
}

func (h *wifiHandler) ListWifiNetworks(ctx context.Context, _ *configv1.ListWifiNetworksRequest) (*configv1.ListWifiNetworksResponse, error) {
	list, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	resp := &configv1.ListWifiNetworksResponse{WifiNetworks: make([]*configv1.WifiNetwork, 0, len(list))}
	for _, w := range list {
		resp.WifiNetworks = append(resp.WifiNetworks, apiconvgen.WifiToProto(w))
	}
	return resp, nil
}

func (h *wifiHandler) UpdateWifiNetwork(ctx context.Context, req *configv1.UpdateWifiNetworkRequest) (*configv1.UpdateWifiNetworkResponse, error) {
	w, err := apiconvgen.WifiFromProto(req.GetWifiNetwork())
	if err != nil {
		return nil, err
	}
	out, err := h.svc.Update(ctx, w, req.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}
	return &configv1.UpdateWifiNetworkResponse{WifiNetwork: apiconvgen.WifiToProto(out)}, nil
}

func (h *wifiHandler) DeleteWifiNetwork(ctx context.Context, req *configv1.DeleteWifiNetworkRequest) (*configv1.DeleteWifiNetworkResponse, error) {
	rev, err := apiconv.ParseEtag(req.GetEtag())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Delete(ctx, req.GetId(), rev); err != nil {
		return nil, err
	}
	return &configv1.DeleteWifiNetworkResponse{}, nil
}

// --- NetworkService ---

type networkHandler struct {
	configv1connect.UnimplementedNetworkServiceHandler
	svc *core.NetworkService
}

func (h *networkHandler) CreateNetwork(ctx context.Context, req *configv1.CreateNetworkRequest) (*configv1.CreateNetworkResponse, error) {
	n, err := apiconvgen.NetworkFromProto(req.GetNetwork())
	if err != nil {
		return nil, err
	}
	n.Revision = 0
	out, err := h.svc.Create(ctx, n)
	if err != nil {
		return nil, err
	}
	return &configv1.CreateNetworkResponse{Network: apiconvgen.NetworkToProto(out)}, nil
}

func (h *networkHandler) GetNetwork(ctx context.Context, req *configv1.GetNetworkRequest) (*configv1.GetNetworkResponse, error) {
	out, err := h.svc.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &configv1.GetNetworkResponse{Network: apiconvgen.NetworkToProto(out)}, nil
}

func (h *networkHandler) ListNetworks(ctx context.Context, _ *configv1.ListNetworksRequest) (*configv1.ListNetworksResponse, error) {
	list, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	resp := &configv1.ListNetworksResponse{Networks: make([]*configv1.Network, 0, len(list))}
	for _, n := range list {
		resp.Networks = append(resp.Networks, apiconvgen.NetworkToProto(n))
	}
	return resp, nil
}

func (h *networkHandler) UpdateNetwork(ctx context.Context, req *configv1.UpdateNetworkRequest) (*configv1.UpdateNetworkResponse, error) {
	n, err := apiconvgen.NetworkFromProto(req.GetNetwork())
	if err != nil {
		return nil, err
	}
	out, err := h.svc.Update(ctx, n, req.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}
	return &configv1.UpdateNetworkResponse{Network: apiconvgen.NetworkToProto(out)}, nil
}

func (h *networkHandler) DeleteNetwork(ctx context.Context, req *configv1.DeleteNetworkRequest) (*configv1.DeleteNetworkResponse, error) {
	rev, err := apiconv.ParseEtag(req.GetEtag())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Delete(ctx, req.GetId(), rev); err != nil {
		return nil, err
	}
	return &configv1.DeleteNetworkResponse{}, nil
}

// --- DeviceService ---

type deviceHandler struct {
	configv1connect.UnimplementedDeviceServiceHandler
	svc *core.DeviceService
}

func (h *deviceHandler) GetDevice(ctx context.Context, req *configv1.GetDeviceRequest) (*configv1.GetDeviceResponse, error) {
	d, err := h.svc.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &configv1.GetDeviceResponse{Device: apiconvgen.DeviceToProto(d)}, nil
}

func (h *deviceHandler) ListDevices(ctx context.Context, req *configv1.ListDevicesRequest) (*configv1.ListDevicesResponse, error) {
	state, err := apiconv.DeviceStateFromProto(req.GetState())
	if err != nil {
		return nil, err
	}
	list, err := h.svc.List(ctx, store.DeviceFilter{State: state, Adapter: req.GetAdapter()})
	if err != nil {
		return nil, err
	}
	return &configv1.ListDevicesResponse{Devices: apiconvgen.DevicesToProto(list)}, nil
}

func (h *deviceHandler) ListPendingDevices(ctx context.Context, req *configv1.ListPendingDevicesRequest) (*configv1.ListPendingDevicesResponse, error) {
	list, err := h.svc.ListPending(ctx, req.GetAdapter())
	if err != nil {
		return nil, err
	}
	return &configv1.ListPendingDevicesResponse{Devices: apiconvgen.DevicesToProto(list)}, nil
}

func (h *deviceHandler) AdoptDevice(ctx context.Context, req *configv1.AdoptDeviceRequest) (*configv1.AdoptDeviceResponse, error) {
	d, err := h.svc.Adopt(ctx, req.GetId(), req.GetMac())
	if err != nil {
		return nil, err
	}
	return &configv1.AdoptDeviceResponse{Device: apiconvgen.DeviceToProto(d)}, nil
}

func (h *deviceHandler) ForgetDevice(ctx context.Context, req *configv1.ForgetDeviceRequest) (*configv1.ForgetDeviceResponse, error) {
	if err := h.svc.Forget(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &configv1.ForgetDeviceResponse{}, nil
}
