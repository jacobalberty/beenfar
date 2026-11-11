package adapterhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jacobalberty/beenfar/api/adapter"
	wiregen "github.com/jacobalberty/beenfar/api/adapter/wire/gen"
	adapterv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/adapter/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/adapter/v1/adapterv1connect"
	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

type adapterKey struct{}

// WithAdapter returns a context for a call made by the adapter called name.
// The server's authentication interceptor sets it from the adapter token.
func WithAdapter(ctx context.Context, name string) context.Context {
	ctx = context.WithValue(ctx, adapterKey{}, name)
	return telemetry.WithAttrs(ctx, slog.String("adapter", name))
}

func adapterFrom(ctx context.Context) (string, error) {
	name, ok := ctx.Value(adapterKey{}).(string)
	if !ok || name == "" {
		return "", auth.ErrUnauthenticated
	}
	return name, nil
}

// Service serves the adapter API (beenfar.adapter.v1.AdapterHostService) to
// external adapters. Each call acts as the adapter named by its token.
type Service struct {
	adapterv1connect.UnimplementedAdapterHostServiceHandler
	reg   *Registry
	store store.Store
}

var _ adapterv1connect.AdapterHostServiceHandler = (*Service)(nil)

// NewService returns the adapter API handler.
func NewService(reg *Registry, st store.Store) *Service { return &Service{reg: reg, store: st} }

// host returns the Host for the calling adapter.
func (s *Service) host(ctx context.Context) (*Host, error) {
	name, err := adapterFrom(ctx)
	if err != nil {
		return nil, err
	}
	return NewHost(name, s.store), nil
}

// rpcErr maps the SDK's not-found error onto the one the error interceptor
// turns into NOT_FOUND. The message stays short: it reaches the adapter.
func rpcErr(err error) error {
	if errors.Is(err, adapter.ErrNotFound) {
		return fmt.Errorf("%w: no such device", store.ErrNotFound)
	}
	return err
}

func (s *Service) Register(ctx context.Context, req *adapterv1.RegisterRequest, stream adapterv1connect.AdapterHostServiceRegisterServerStream) error {
	name, err := adapterFrom(ctx)
	if err != nil {
		return err
	}
	sess, err := s.reg.Connect(name, req.GetVersion())
	if err != nil {
		return err
	}
	defer sess.Close()
	slog.InfoContext(ctx, "adapter connected", slog.String("version", req.GetVersion()))
	defer func() {
		slog.InfoContext(context.WithoutCancel(ctx), "adapter disconnected", slog.String("adapter", name))
	}()

	if err := stream.Send(&adapterv1.RegisterResponse{Event: &adapterv1.RegisterResponse_Registered{Registered: &adapterv1.Registered{}}}); err != nil {
		return err
	}
	for {
		select {
		case cmd := <-sess.Commands():
			msg := &adapterv1.RegisterResponse{CommandId: cmd.ID, TraceContext: cmd.Trace}
			dev := wiregen.DeviceToProto(cmd.Device)
			switch cmd.Kind {
			case CommandAdopt:
				msg.Event = &adapterv1.RegisterResponse_Adopt{Adopt: dev}
			case CommandForget:
				msg.Event = &adapterv1.RegisterResponse_Forget{Forget: dev}
			}
			if err := stream.Send(msg); err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (s *Service) CompleteCommand(ctx context.Context, req *adapterv1.CompleteCommandRequest) (*adapterv1.CompleteCommandResponse, error) {
	name, err := adapterFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.reg.CompleteCommand(name, req.GetCommandId(), req.GetError()); err != nil {
		return nil, err
	}
	return &adapterv1.CompleteCommandResponse{}, nil
}

func (s *Service) LookupDevice(ctx context.Context, req *adapterv1.LookupDeviceRequest) (*adapterv1.LookupDeviceResponse, error) {
	h, err := s.host(ctx)
	if err != nil {
		return nil, err
	}
	d, err := h.DeviceByMAC(ctx, req.GetMac())
	if err != nil {
		return nil, rpcErr(err)
	}
	return &adapterv1.LookupDeviceResponse{Device: wiregen.DeviceToProto(d)}, nil
}

func (s *Service) ReportDevice(ctx context.Context, req *adapterv1.ReportDeviceRequest) (*adapterv1.ReportDeviceResponse, error) {
	h, err := s.host(ctx)
	if err != nil {
		return nil, err
	}
	d, err := h.ReportDevice(ctx, wiregen.DiscoveredFromProto(req.GetDiscovered()))
	if err != nil {
		return nil, rpcErr(err)
	}
	return &adapterv1.ReportDeviceResponse{Device: wiregen.DeviceToProto(d)}, nil
}

func (s *Service) GetDesiredConfig(ctx context.Context, req *adapterv1.GetDesiredConfigRequest) (*adapterv1.GetDesiredConfigResponse, error) {
	h, err := s.host(ctx)
	if err != nil {
		return nil, err
	}
	c, err := h.DesiredConfig(ctx, req.GetDeviceId())
	if err != nil {
		return nil, rpcErr(err)
	}
	return &adapterv1.GetDesiredConfigResponse{Config: wiregen.DesiredConfigToProto(c)}, nil
}

func (s *Service) ReportState(ctx context.Context, req *adapterv1.ReportStateRequest) (*adapterv1.ReportStateResponse, error) {
	h, err := s.host(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.ReportState(ctx, req.GetDeviceId(), wiregen.StateFromProto(req.GetState())); err != nil {
		return nil, rpcErr(err)
	}
	return &adapterv1.ReportStateResponse{}, nil
}

func (s *Service) GetDeviceSecret(ctx context.Context, req *adapterv1.GetDeviceSecretRequest) (*adapterv1.GetDeviceSecretResponse, error) {
	h, err := s.host(ctx)
	if err != nil {
		return nil, err
	}
	secret, err := h.DeviceSecret(ctx, req.GetDeviceId(), req.GetName())
	if errors.Is(err, adapter.ErrNoSecret) {
		return &adapterv1.GetDeviceSecretResponse{}, nil
	}
	if err != nil {
		return nil, rpcErr(err)
	}
	return &adapterv1.GetDeviceSecretResponse{Found: true, Secret: secret}, nil
}

func (s *Service) SetDeviceSecret(ctx context.Context, req *adapterv1.SetDeviceSecretRequest) (*adapterv1.SetDeviceSecretResponse, error) {
	h, err := s.host(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.SetDeviceSecret(ctx, req.GetDeviceId(), req.GetName(), req.GetSecret()); err != nil {
		return nil, rpcErr(err)
	}
	return &adapterv1.SetDeviceSecretResponse{}, nil
}
