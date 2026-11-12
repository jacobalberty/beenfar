package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/otelconnect"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	wiregen "github.com/jacobalberty/beenfar/api/adapter/wire/gen"
	adapterv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/adapter/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/adapter/v1/adapterv1connect"
)

// RemoteOption configures RunRemote.
type RemoteOption func(*remoteConfig)

type remoteConfig struct {
	httpClient connecthttp.HTTPClient
	version    string
	tracers    trace.TracerProvider
	prop       propagation.TextMapPropagator
	slogHook   bool
	minBackoff time.Duration
	maxBackoff time.Duration
}

// WithHTTPClient sets the HTTP client used to reach the controller.
func WithHTTPClient(c *http.Client) RemoteOption {
	return func(rc *remoteConfig) { rc.httpClient = c }
}

// WithVersion sets the version shown in the controller's adapter list.
func WithVersion(v string) RemoteOption { return func(rc *remoteConfig) { rc.version = v } }

// WithTracerProvider sets the tracer provider (default: the global one).
func WithTracerProvider(tp trace.TracerProvider) RemoteOption {
	return func(rc *remoteConfig) { rc.tracers = tp }
}

// WithPropagator sets the trace propagator (default: the global one).
func WithPropagator(p propagation.TextMapPropagator) RemoteOption {
	return func(rc *remoteConfig) { rc.prop = p }
}

// WithoutSlogHandler stops RunRemote from wrapping the default slog handler
// with NewLogHandler.
func WithoutSlogHandler() RemoteOption { return func(rc *remoteConfig) { rc.slogHook = false } }

// WithReconnectBackoff sets the first and the longest wait before
// reconnecting after the command stream drops.
func WithReconnectBackoff(first, longest time.Duration) RemoteOption {
	return func(rc *remoteConfig) { rc.minBackoff, rc.maxBackoff = first, longest }
}

// RunRemote runs impl as an external adapter: it connects to the controller
// at url with the adapter's token, registers, starts impl with a Host backed
// by the adapter API, and carries out the Adopt and Forget commands the
// controller sends. It returns nil once ctx is done, and an error if the
// controller refuses the token or impl.Start fails.
//
// If the command stream drops, RunRemote reconnects with backoff; impl keeps
// running and its Host keeps working across reconnects.
//
// Unless WithoutSlogHandler is given, RunRemote replaces the default slog
// handler with one that wraps it (see NewLogHandler), so log lines written
// with the *Context methods carry the trace they belong to.
//
// Calls to the controller go through an otelconnect client interceptor, and
// each command continues the trace of the request that caused it, so a trace
// that starts at the API continues inside the adapter.
func RunRemote(ctx context.Context, url, token string, impl Adapter, opts ...RemoteOption) error {
	cfg := remoteConfig{
		httpClient: http.DefaultClient,
		slogHook:   true,
		minBackoff: time.Second,
		maxBackoff: 30 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.tracers == nil {
		cfg.tracers = otel.GetTracerProvider()
	}
	if cfg.prop == nil {
		cfg.prop = otel.GetTextMapPropagator()
	}
	if cfg.slogHook {
		slog.SetDefault(slog.New(NewLogHandler(slog.Default().Handler())))
	}
	ctx = WithLogAttrs(ctx, slog.String("adapter", impl.Name()))

	otelInterceptor, err := otelconnect.NewClientInterceptor(
		otelconnect.WithTracerProvider(cfg.tracers),
		otelconnect.WithPropagator(cfg.prop),
	)
	if err != nil {
		return fmt.Errorf("otelconnect: %w", err)
	}
	bearer := func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, spec)
		}
	}
	client := adapterv1connect.NewAdapterHostServiceClient(
		connect.NewClient(connecthttp.NewTransport(cfg.httpClient, url), bearer, otelInterceptor))
	r := &remote{cfg: cfg, impl: impl, client: client, host: remoteHost{client}, tracer: cfg.tracers.Tracer("github.com/jacobalberty/beenfar/api/adapter")}
	return r.run(ctx)
}

type remote struct {
	cfg    remoteConfig
	impl   Adapter
	client adapterv1connect.AdapterHostServiceClient
	host   remoteHost
	tracer trace.Tracer

	runCtx    context.Context // lives as long as RunRemote; impl.Start runs under it
	fail      context.CancelCauseFunc
	startOnce sync.Once
}

func (r *remote) run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	r.runCtx, r.fail = ctx, cancel
	backoff := r.cfg.minBackoff
	for {
		connected, err := r.session(ctx)
		if ctx.Err() != nil {
			// Done: either the caller cancelled, or impl.Start failed.
			if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
				return cause
			}
			return nil
		}
		if isFatal(err) {
			return err
		}
		if connected {
			backoff = r.cfg.minBackoff
		}
		slog.WarnContext(ctx, "adapter stream ended; reconnecting", slog.Any("error", err), slog.Duration("in", backoff))
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil
		}
		backoff = min(backoff*2, r.cfg.maxBackoff)
	}
}

func isFatal(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return true
	}
	return false
}

// session holds one command stream open until it ends. connected reports
// whether registration succeeded.
func (r *remote) session(ctx context.Context) (connected bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := r.client.Register(ctx, &adapterv1.RegisterRequest{Version: r.cfg.version})
	if err != nil {
		return false, err
	}
	defer func() { _ = stream.Close() }()

	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel() // runs first: unblocks commands still waiting on the controller
	for {
		msg, err := stream.Receive()
		if err != nil {
			return connected, err
		}
		if msg.GetRegistered() != nil {
			connected = true
			slog.InfoContext(ctx, "adapter registered")
			r.startOnce.Do(func() {
				go func() {
					// impl runs for the life of the process, not of one stream.
					if err := r.impl.Start(r.runCtx, r.host); err != nil && r.runCtx.Err() == nil {
						r.fail(fmt.Errorf("adapter %q: %w", r.impl.Name(), err))
					}
				}()
			})
			continue
		}
		wg.Go(func() { r.handle(ctx, msg) })
	}
}

// handle carries out one command and reports the outcome.
func (r *remote) handle(ctx context.Context, msg *adapterv1.RegisterResponse) {
	ctx = r.cfg.prop.Extract(ctx, propagation.MapCarrier(msg.GetTraceContext()))
	var (
		verb string
		dev  Device
		run  func(context.Context, Device) error
	)
	switch ev := msg.GetEvent().(type) {
	case *adapterv1.RegisterResponse_Adopt:
		verb, dev, run = "Adopt", wiregen.DeviceFromProto(ev.Adopt), r.impl.Adopt
	case *adapterv1.RegisterResponse_Forget:
		verb, dev, run = "Forget", wiregen.DeviceFromProto(ev.Forget), r.impl.Forget
	default:
		return
	}
	ctx = WithLogAttrs(ctx, slog.String("device.id", dev.ID), slog.String("device.mac", dev.MAC))
	ctx, span := r.tracer.Start(ctx, "adapter."+verb, trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("adapter", r.impl.Name()), attribute.String("device.id", dev.ID), attribute.String("device.mac", dev.MAC)))
	defer span.End()

	var (
		msgErr   string
		deferred bool
	)
	if err := run(ctx, dev); errors.Is(err, ErrForgetDeferred) {
		deferred = true
	} else if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.WarnContext(ctx, "command failed", slog.String("command", verb), slog.Any("error", err))
		msgErr = err.Error()
	}
	if _, err := r.client.CompleteCommand(ctx, &adapterv1.CompleteCommandRequest{CommandId: msg.GetCommandId(), Error: msgErr, Deferred: deferred}); err != nil {
		span.RecordError(err)
		slog.WarnContext(ctx, "reporting command result failed", slog.String("command", verb), slog.Any("error", err))
	}
}

// remoteHost is a Host backed by the adapter API.
type remoteHost struct {
	c adapterv1connect.AdapterHostServiceClient
}

var _ Host = remoteHost{}

func (h remoteHost) DeviceByMAC(ctx context.Context, mac string) (Device, error) {
	res, err := h.c.LookupDevice(ctx, &adapterv1.LookupDeviceRequest{Mac: mac})
	if err != nil {
		return Device{}, mapRemoteErr(err)
	}
	return wiregen.DeviceFromProto(res.GetDevice()), nil
}

func (h remoteHost) ReportDevice(ctx context.Context, d Discovered) (Device, error) {
	res, err := h.c.ReportDevice(ctx, &adapterv1.ReportDeviceRequest{Discovered: wiregen.DiscoveredToProto(d)})
	if err != nil {
		return Device{}, mapRemoteErr(err)
	}
	return wiregen.DeviceFromProto(res.GetDevice()), nil
}

func (h remoteHost) DesiredConfig(ctx context.Context, deviceID string) (DesiredConfig, error) {
	res, err := h.c.GetDesiredConfig(ctx, &adapterv1.GetDesiredConfigRequest{DeviceId: deviceID})
	if err != nil {
		return DesiredConfig{}, mapRemoteErr(err)
	}
	return wiregen.DesiredConfigFromProto(res.GetConfig()), nil
}

func (h remoteHost) ReportState(ctx context.Context, deviceID string, s State) error {
	_, err := h.c.ReportState(ctx, &adapterv1.ReportStateRequest{DeviceId: deviceID, State: wiregen.StateToProto(s)})
	return mapRemoteErr(err)
}

func (h remoteHost) DeviceSecret(ctx context.Context, deviceID, name string) ([]byte, error) {
	res, err := h.c.GetDeviceSecret(ctx, &adapterv1.GetDeviceSecretRequest{DeviceId: deviceID, Name: name})
	if err != nil {
		return nil, mapRemoteErr(err)
	}
	if !res.GetFound() {
		return nil, ErrNoSecret
	}
	return res.GetSecret(), nil
}

func (h remoteHost) SetDeviceSecret(ctx context.Context, deviceID, name string, secret []byte) error {
	_, err := h.c.SetDeviceSecret(ctx, &adapterv1.SetDeviceSecretRequest{DeviceId: deviceID, Name: name, Secret: secret})
	return mapRemoteErr(err)
}

// mapRemoteErr turns NOT_FOUND into ErrNotFound, so adapters test the same
// errors whether they run built in or remote.
func mapRemoteErr(err error) error {
	if err != nil && connect.CodeOf(err) == connect.CodeNotFound {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}
