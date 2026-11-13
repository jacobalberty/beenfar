// Package unifi is the built-in adapter for UniFi devices. It serves the
// device-facing /inform endpoint on go-unifi's inform handler and keeps each
// device's keys and configuration revision in step with the controller.
//
// # Devices
//
// An inform from an unknown MAC is reported to the controller, which records
// a pending device, and answered with 404, which makes the device keep
// trying. Adoption is explicit: Adopt stores a new key for the device as its
// pending key. The device's next inform is answered with a SetParam carrying
// that key, sealed with the key the device used. When the device switches
// and informs under the pending key, the key is committed and the others are
// deleted. If the reply is lost the device keeps using its old key, which
// stays accepted, so it simply gets the reply again.
//
// An adopted device is sent a SetParam whenever the config revision it
// reports differs from the controller's, and a Noop with the inform interval
// otherwise.
//
// # Reset devices
//
// An adopted device that comes back speaking the default key was factory
// reset. It is the same managed device, so it stays adopted, but it is not
// re-keyed by itself: the default key is public, so a reply sealed with it
// reaches anybody, and anyone on the network can send such an inform for any
// MAC. The device is marked as awaiting re-adoption and answered 404 until
// someone accepts that (AdoptDevice, as for a new device); then it is re-keyed
// like a new one and its readoption is in progress until it uses the new key.
// A device that turns out to be running on its own key drops the key that was
// meant for it. Only a device silent for Options.ReadoptAfter is considered,
// so a live device is never flagged by a spoofed inform.
//
// # Forgetting devices
//
// Forgetting an adopted device factory resets it. Forget answers
// adapter.ErrForgetDeferred, and the controller keeps the device, marked as
// forgetting, so that its keys stay. The device's next inform, on its own key,
// is answered with a SetDefault, which makes it reset itself; it then informs
// on the default key, which is taken for confirmation (the controller is told
// to drop the device, so it comes back as a new, pending one). Until then it is
// sent the SetDefault on every inform. A device that never comes back stays as
// it is until the controller gives up (Forget with Device.Forgetting set,
// which drops the keys: the device is then locked until it is reset by hand).
//
// # Taking over devices
//
// Keys taken from another controller are stored as imported keys (see
// ImportKeys). A device still managed there then decrypts, appears as
// pending with its real model, and is re-keyed when adopted. A device no key
// opens is recorded as locked.
package unifi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jacobalberty/go-unifi/inform"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/jacobalberty/beenfar/api/adapter"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

// Name is the adapter's name, recorded on every device it reports.
const Name = "unifi"

// DefaultInterval is how many seconds a device waits between informs.
const DefaultInterval = 10

// Options configures New.
type Options struct {
	// Tracer starts the span for each inform. Nil disables tracing.
	Tracer trace.Tracer
	// Meter exports the adapter's counters. Nil disables them.
	Meter metric.Meter
	// Interval is the inform interval in seconds (default DefaultInterval).
	Interval int
	// ReadoptAfter is how long an adopted device must have been silent before
	// one that then speaks the default key is taken for factory reset and
	// offered for re-adoption (see the package comment). Zero turns that off:
	// such a device stays locked. Keep it well above the inform interval, so
	// that a slow device is never mistaken for a reset one.
	ReadoptAfter time.Duration
	// UseGCM tells devices adopted from now on to switch to AES-GCM. It is off
	// by default: GCM is verified against one real capture of a device's
	// inform, but not of a controller's reply.
	UseGCM bool
}

// Adapter is the UniFi adapter. It implements adapter.Adapter and
// adapter.HTTPProvider.
type Adapter struct {
	opts   Options
	tracer trace.Tracer
	host   atomic.Pointer[adapter.Host]

	informs  metric.Int64Counter
	rejected metric.Int64Counter
	pushes   metric.Int64Counter
}

var (
	_ adapter.Adapter      = (*Adapter)(nil)
	_ adapter.HTTPProvider = (*Adapter)(nil)
)

// New returns the adapter.
func New(opts Options) (*Adapter, error) {
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	tr := opts.Tracer
	if tr == nil {
		tr = noop.NewTracerProvider().Tracer("")
	}
	a := &Adapter{opts: opts, tracer: tr}
	m := opts.Meter
	if m == nil {
		m = metricnoop.NewMeterProvider().Meter("")
	}
	var err error
	if a.informs, err = m.Int64Counter("beenfar.unifi.informs.received",
		metric.WithDescription("Informs received from UniFi devices."), metric.WithUnit("{inform}")); err != nil {
		return nil, err
	}
	if a.rejected, err = m.Int64Counter("beenfar.unifi.informs.rejected",
		metric.WithDescription("Informs not answered with a reply, by reason."), metric.WithUnit("{inform}")); err != nil {
		return nil, err
	}
	if a.pushes, err = m.Int64Counter("beenfar.unifi.config_pushes",
		metric.WithDescription("SetParam and SetDefault replies sent to adopted devices, by reason."), metric.WithUnit("{push}")); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Adapter) Name() string { return Name }

// Start gives the adapter its Host and blocks until ctx is done. Informs are
// refused until it has run.
func (a *Adapter) Start(ctx context.Context, h adapter.Host) error {
	a.host.Store(&h)
	<-ctx.Done()
	return ctx.Err()
}

// HTTPHandlers mounts the inform endpoint.
func (a *Adapter) HTTPHandlers() map[string]http.Handler {
	return map[string]http.Handler{"POST /inform": http.HandlerFunc(a.serveInform)}
}

// Adopt starts the adoption of a device: it stores a new pending key, which
// the device's next inform is answered with.
func (a *Adapter) Adopt(ctx context.Context, d adapter.Device) error {
	return a.Rekey(ctx, d)
}

// Rekey gives an adopted or adopting device a new key, by the same route as
// adoption: the new key is stored as pending and sent to the device with its
// next inform, and replaces the current key once the device uses it.
func (a *Adapter) Rekey(ctx context.Context, d adapter.Device) error {
	h, err := a.hostOrErr()
	if err != nil {
		return err
	}
	return h.SetDeviceSecret(ctx, d.ID, SecretPending, inform.NewKey())
}

// Forget resets an adopted device: it answers adapter.ErrForgetDeferred and,
// on the device's next inform, tells it to factory reset itself (see
// forgetting). The device's keys stay until then, since the command is sealed
// with them. When the controller gives up waiting (d.Forgetting is already
// set) or there is nothing to reset, Forget drops all of the device's keys.
func (a *Adapter) Forget(ctx context.Context, d adapter.Device) error {
	h, err := a.hostOrErr()
	if err != nil {
		return err
	}
	if d.State == adapter.DeviceAdopted && !d.Forgetting {
		return adapter.ErrForgetDeferred
	}
	for _, name := range secretNames {
		if err := h.SetDeviceSecret(ctx, d.ID, name, nil); err != nil && !errors.Is(err, adapter.ErrNotFound) {
			return err
		}
	}
	return nil
}

var errNotStarted = errors.New("unifi adapter not started")

func (a *Adapter) hostOrErr() (adapter.Host, error) {
	h := a.host.Load()
	if h == nil {
		return nil, errNotStarted
	}
	return *h, nil
}

type remoteIPKey struct{}

// serveInform runs one inform under its own span.
func (a *Adapter) serveInform(w http.ResponseWriter, r *http.Request) {
	ctx, span := a.tracer.Start(r.Context(), "unifi.inform", trace.WithAttributes(attribute.String("unifi.response", "error")))
	defer span.End()
	ctx = context.WithValue(ctx, remoteIPKey{}, remoteIP(r))
	a.informs.Add(ctx, 1)
	h := &inform.Handler{
		Keys:          a.keys,
		Serve:         a.serve,
		Undecryptable: a.undecryptable,
		OnError:       a.onError,
	}
	h.ServeHTTP(w, r.WithContext(ctx))
}

func (a *Adapter) keys(ctx context.Context, mac net.HardwareAddr) ([]inform.Key, error) {
	h, err := a.hostOrErr()
	if err != nil {
		return nil, err
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("device.mac", mac.String()))
	return keysFor(ctx, h, mac.String(), a.opts.ReadoptAfter, time.Now())
}

func (a *Adapter) undecryptable(ctx context.Context, p *inform.Packet) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("device.mac", p.MAC.String()),
		attribute.String("unifi.flags", p.Flags.String()),
		attribute.String("unifi.response", "undecryptable"),
	)
	ctx = telemetry.WithAttrs(ctx, slog.String("device.mac", p.MAC.String()))
	h, err := a.hostOrErr()
	if err != nil {
		return
	}
	if _, err := h.ReportDevice(ctx, adapter.Discovered{MAC: p.MAC.String(), IP: ipFrom(ctx), Locked: true}); err != nil {
		slog.WarnContext(ctx, "recording locked device", slog.Any("error", err))
		return
	}
	slog.InfoContext(ctx, "inform no key opens; device is locked until its credentials are imported")
}

func (a *Adapter) onError(ctx context.Context, mac net.HardwareAddr, status int, err error) {
	a.rejected.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", rejectReason(status, err))))
	span := trace.SpanFromContext(ctx)
	if status >= 500 {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "inform failed", slog.Int("status", status), slog.Any("error", err))
		return
	}
	slog.InfoContext(ctx, "inform rejected", slog.Int("status", status), slog.Any("error", err))
}

// errAwaitingReadoption answers a reset device nobody has accepted yet: 404,
// so that it keeps trying.
var errAwaitingReadoption = fmt.Errorf("%w: reset device awaiting re-adoption", inform.ErrUnknownDevice)

// rejectReason classifies why an inform got no reply.
func rejectReason(status int, err error) string {
	switch {
	case errors.Is(err, errAwaitingReadoption):
		return "awaiting_readoption"
	case errors.Is(err, inform.ErrUnknownDevice):
		return "not_adopted"
	case errors.Is(err, inform.ErrDecrypt):
		return "undecryptable"
	case status >= 500:
		return "error"
	}
	return "malformed"
}

// serve answers a decoded inform.
func (a *Adapter) serve(ctx context.Context, p *inform.Packet, in *inform.Inform, used inform.Key) (inform.Response, error) {
	span := trace.SpanFromContext(ctx)
	mac := p.MAC.String()
	span.SetAttributes(attribute.String("device.mac", mac), attribute.String("unifi.flags", p.Flags.String()))
	ctx = telemetry.WithAttrs(ctx, slog.String("device.mac", mac))

	h, err := a.hostOrErr()
	if err != nil {
		return nil, err
	}
	resp, err := a.respond(ctx, h, p, in, used)
	switch {
	case errors.Is(err, errAwaitingReadoption):
		span.SetAttributes(attribute.String("unifi.response", "awaiting_readoption"))
	case errors.Is(err, inform.ErrUnknownDevice):
		span.SetAttributes(attribute.String("unifi.response", "not_found"))
	case err == nil:
		kind := "noop"
		switch resp.(type) {
		case inform.SetParam:
			kind = "setparam"
		case inform.SetDefault:
			kind = "setdefault"
		}
		span.SetAttributes(attribute.String("unifi.response", kind))
	}
	return resp, err
}

func (a *Adapter) respond(ctx context.Context, h adapter.Host, p *inform.Packet, in *inform.Inform, used inform.Key) (inform.Response, error) {
	mac := p.MAC.String()
	dev, err := h.DeviceByMAC(ctx, mac)
	if err != nil && !errors.Is(err, adapter.ErrNotFound) {
		return nil, err
	}
	if errors.Is(err, adapter.ErrNotFound) {
		dev = adapter.Device{}
	}

	// Refresh what the controller knows. A pending device is reported with
	// the model it sent, which, when it decrypted with an imported key, is
	// its real one.
	if dev.ID == "" || dev.State == adapter.DevicePending {
		kind, ports := classify(in)
		if _, err := h.ReportDevice(ctx, adapter.Discovered{
			MAC: mac, Model: in.Model, IP: ipFrom(ctx), Firmware: in.Version,
			Kind: kind, PortCount: ports,
		}); err != nil {
			return nil, err
		}
		if dev.ID == "" {
			slog.InfoContext(ctx, "new adoption request", slog.String("model", in.Model))
		}
		// Not adopted: devices keep retrying until adopted.
		return nil, inform.ErrUnknownDevice
	}

	// Adopted from here on.
	ctx = telemetry.WithAttrs(ctx, slog.String("device.id", dev.ID))
	if dev.Forgetting {
		return a.forgetting(ctx, h, dev, used)
	}
	pending, err := h.DeviceSecret(ctx, dev.ID, SecretPending)
	switch {
	case errors.Is(err, adapter.ErrNoSecret):
		pending = nil
	case err != nil:
		return nil, err
	}
	_, err = h.DeviceSecret(ctx, dev.ID, SecretCurrent)
	hasCurrent := err == nil
	if err != nil && !errors.Is(err, adapter.ErrNoSecret) {
		return nil, err
	}
	onDefault := used.Name == keyDefault || len(used.Bytes) == 0
	readoption := adapter.ReadoptionNone

	switch {
	case used.Name == SecretPending:
		// The device switched to the key it was sent: commit it.
		if err := commitKey(ctx, h, dev.ID, used.Bytes); err != nil {
			return nil, err
		}
		pending = nil
		slog.InfoContext(ctx, "device switched to its new key")
	case !onDefault && dev.Readoption != adapter.ReadoptionNone && pending != nil:
		// The device was thought reset but runs on a key of its own. The key
		// it was going to be given was sealed with the default key, which
		// anybody can open: drop it rather than hand it to the device.
		if err := h.SetDeviceSecret(ctx, dev.ID, SecretPending, nil); err != nil {
			return nil, err
		}
		pending = nil
		slog.InfoContext(ctx, "device is running on its own key again; dropped the key meant for a reset device")
	case onDefault && hasCurrent:
		// It had a key and speaks the default one: it was reset. It stays the
		// same adopted device, but nothing is sent to it until somebody has
		// accepted re-adopting it.
		if dev.Readoption != adapter.ReadoptionInProgress {
			readoption = adapter.ReadoptionAwaiting
			if dev.Readoption != adapter.ReadoptionAwaiting {
				slog.WarnContext(ctx, "adopted device is back on the default key; it was reset and awaits re-adoption")
			}
		} else {
			readoption = adapter.ReadoptionInProgress
		}
	}

	kind, ports := classify(in)
	if err := h.ReportState(ctx, dev.ID, adapter.State{
		IP: ipFrom(ctx), Firmware: in.Version, Model: in.Model,
		Kind: kind, PortCount: ports,
		ConfigRevision: parseRevision(in.ConfigVersion),
		Readoption:     readoption,
	}); err != nil {
		return nil, err
	}
	if readoption == adapter.ReadoptionAwaiting {
		return nil, errAwaitingReadoption
	}

	if onDefault && pending == nil {
		// Adopted, yet still on the default key and with no key on its way
		// (for instance adopted before a restart lost the pending key, or
		// re-adoption accepted): give it one now.
		key := inform.NewKey()
		if err := h.SetDeviceSecret(ctx, dev.ID, SecretPending, key); err != nil {
			return nil, err
		}
		pending = key
	}
	desired, err := h.DesiredConfig(ctx, dev.ID)
	if err != nil {
		return nil, err
	}
	version := formatRevision(desired.Revision)

	// The key the device should end up with: the pending one while it has
	// not switched, else the one it just used.
	authKey := used.Bytes
	if pending != nil {
		authKey = pending
	}
	if pending == nil && !onDefault && in.ConfigVersion == version {
		return inform.Noop{Interval: a.opts.Interval}, nil
	}
	reason := "revision"
	switch {
	case readoption == adapter.ReadoptionInProgress:
		reason = "readopt"
	case pending != nil || onDefault:
		reason = "key"
	}
	a.pushes.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
	// Settings carry secrets (wifi keys, RADIUS secrets), so they only go out
	// once the device speaks a key of its own: the replies that hand it that
	// key are sealed with the default key, which anybody can open.
	return a.setParam(ctx, authKey, version, desired, kind, pending == nil && !onDefault)
}

// forgetting answers a device the controller is forgetting (see Forget). On a
// key of its own it is sent a SetDefault, every time it informs until it has
// switched, so a lost reply is harmless. Once it speaks the default key it has
// reset: the controller is told to drop it, and it is answered 404, so that its
// next inform makes it a new, pending device. A default-key inform only counts
// as the reset if the device was told to reset (the mark in SecretReset) or was
// already awaiting re-adoption: anybody can send one, and acting on it would let
// them make the controller drop a device that has not reset.
func (a *Adapter) forgetting(ctx context.Context, h adapter.Host, dev adapter.Device, used inform.Key) (inform.Response, error) {
	if used.Name == keyDefault || len(used.Bytes) == 0 {
		_, err := h.DeviceSecret(ctx, dev.ID, SecretReset)
		told := err == nil
		if err != nil && !errors.Is(err, adapter.ErrNoSecret) {
			return nil, err
		}
		if !told && dev.Readoption == adapter.ReadoptionNone {
			slog.InfoContext(ctx, "ignored a default-key inform from a device being forgotten that was not told to reset")
			return nil, inform.ErrUnknownDevice
		}
		if err := h.ReportState(ctx, dev.ID, adapter.State{Released: true}); err != nil {
			return nil, err
		}
		slog.InfoContext(ctx, "device was reset and released")
		return nil, inform.ErrUnknownDevice
	}
	if err := h.SetDeviceSecret(ctx, dev.ID, SecretReset, []byte(time.Now().UTC().Format(time.RFC3339))); err != nil {
		return nil, err
	}
	a.pushes.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "reset")))
	slog.InfoContext(ctx, "telling the device to factory reset")
	return inform.SetDefault{}, nil
}

// setParam builds the reply that gives the device authKey and the desired
// configuration.
func (a *Adapter) setParam(ctx context.Context, authKey []byte, version string, desired adapter.DesiredConfig, kind adapter.DeviceKind, secure bool) (inform.Response, error) {
	provision := kind == adapter.KindAccessPoint
	if provision && !secure {
		// A key-only reply: it hands the device its key and nothing that
		// must stay secret. It carries a revision no configuration has, so the
		// device keeps asking and is sent its settings once it speaks that key.
		version = formatRevision(0)
	}
	mgmt, err := inform.MgmtConfig{AuthKey: authKey, CfgVersion: version, UseAESGCM: a.opts.UseGCM}.Config()
	if err != nil {
		return nil, fmt.Errorf("rendering mgmt_cfg: %w", err)
	}
	sp := inform.SetParam{ManagementConfig: mgmt.String(), ConfigVersion: version}
	// Access points are provisioned with their wifi, VLAN and RADIUS settings
	// once they are adopted and on their own key. Other devices have none to
	// receive.
	if !provision || !secure {
		return sp, nil
	}
	system, err := renderSystemConfig(desired)
	if err != nil {
		// A configuration that cannot be rendered (an open network, say) must
		// not keep the device from finishing its adoption: it still gets its
		// key and revision, and the error is logged on every push.
		slog.WarnContext(ctx, "not sending the access point its settings: the configuration cannot be rendered", slog.Any("error", err))
		return sp, nil
	}
	if system.Len() > 0 {
		sp.SystemConfig = system.String()
	}
	return sp, nil
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func ipFrom(ctx context.Context) string {
	ip, _ := ctx.Value(remoteIPKey{}).(string)
	return ip
}
