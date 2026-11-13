package adapterhost

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"

	"github.com/jacobalberty/beenfar/api/adapter"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
)

// commandTimeout bounds how long an adapter has to carry out Adopt or Forget.
const commandTimeout = 10 * time.Second

// Driver carries commands to one connected adapter. Built-in adapters are
// drivers themselves; external ones are driven over the adapter API.
type Driver interface {
	Adopt(ctx context.Context, d adapter.Device) error
	Forget(ctx context.Context, d adapter.Device) error
}

type entry struct {
	status core.AdapterStatus
	driver Driver
}

// Registry tracks the connected adapters. It implements core.AdapterRegistry.
type Registry struct {
	store store.Store
	prop  propagation.TextMapPropagator
	now   func() time.Time

	autoAdopt bool
	adopting  sync.Map // device ids with an automatic adoption under way

	adoptions metric.Int64Counter // nil without a meter
	wg        sync.WaitGroup
	mu        sync.RWMutex
	adapters  map[string]*entry
	handlers  map[string]http.Handler
	pending   map[string]*pendingCommand // by command id
}

var _ core.AdapterRegistry = (*Registry)(nil)

// Options configures NewRegistry.
type Options struct {
	Store store.Store
	// Meter, if set, exports the connected-adapters gauge.
	Meter metric.Meter
	// Propagator carries the trace of the request behind an Adopt or Forget
	// to the external adapter that carries it out. Nil sends no trace.
	Propagator propagation.TextMapPropagator
	// AutoAdopt adopts access points as soon as they are seen, so a new one
	// only needs to be plugged in. Other devices (switches, gateways, and
	// anything whose adapter cannot say what it is) stay pending until
	// somebody adopts them.
	AutoAdopt bool
}

// NewRegistry returns an empty registry.
func NewRegistry(opts Options) (*Registry, error) {
	r := &Registry{
		store:     opts.Store,
		prop:      opts.Propagator,
		autoAdopt: opts.AutoAdopt,
		now:       time.Now,
		adapters:  map[string]*entry{},
		handlers:  map[string]http.Handler{},
		pending:   map[string]*pendingCommand{},
	}
	if opts.Meter != nil {
		if err := r.registerMetrics(opts.Meter); err != nil {
			return nil, err
		}
		g, err := opts.Meter.Int64ObservableGauge("beenfar.adapters.connected",
			metric.WithDescription("Adapters currently running or connected."),
			metric.WithUnit("{adapter}"))
		if err != nil {
			return nil, err
		}
		_, err = opts.Meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
			r.mu.RLock()
			defer r.mu.RUnlock()
			var n int64
			for _, e := range r.adapters {
				if e.status.Healthy {
					n++
				}
			}
			o.ObserveInt64(g, n)
			return nil
		}, g)
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}

// AddBuiltin registers a built-in adapter and mounts its HTTP handlers. Call
// Start to run it. Names must be unique.
func (r *Registry) AddBuiltin(a adapter.Adapter) error {
	name := a.Name()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.adapters[name]; ok {
		return fmt.Errorf("adapter %q is already registered", name)
	}
	if p, ok := a.(adapter.HTTPProvider); ok {
		for pattern, h := range p.HTTPHandlers() {
			if _, dup := r.handlers[pattern]; dup {
				return fmt.Errorf("adapter %q: route %q is already taken", name, pattern)
			}
			r.handlers[pattern] = h
		}
	}
	r.adapters[name] = &entry{
		status: core.AdapterStatus{Name: name, Kind: core.AdapterBuiltin},
		driver: a,
	}
	return nil
}

// HTTPHandlers returns the device-facing routes of the built-in adapters,
// keyed by ServeMux pattern.
func (r *Registry) HTTPHandlers() map[string]http.Handler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]http.Handler, len(r.handlers))
	maps.Copy(out, r.handlers)
	return out
}

// Start starts every built-in adapter with an in-process Host and returns
// at once; they are healthy when it does. An adapter that fails is marked
// unhealthy with its error and the others keep running. Wait blocks until
// they have all returned, which happens once ctx is done.
func (r *Registry) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, e := range r.adapters {
		a, ok := e.driver.(adapter.Adapter)
		if !ok {
			continue
		}
		e.status.Healthy = true
		e.status.StatusMessage = ""
		e.status.ConnectTime = r.now()
		r.wg.Go(func() {
			err := a.Start(ctx, r.NewHost(name))
			if err != nil && !errors.Is(err, context.Canceled) {
				slog.ErrorContext(ctx, "adapter stopped", slog.String("adapter", name), slog.Any("error", err))
				r.setHealth(name, false, err.Error())
				return
			}
			r.setHealth(name, false, "")
		})
	}
}

// NewHost returns the Host for the adapter called name, wired to this
// registry's policies (automatic adoption).
func (r *Registry) NewHost(name string) *Host {
	h := NewHost(name, r.store)
	if r.autoAdopt {
		h.onNew = r.maybeAdopt
	}
	return h
}

// maybeAdopt adopts a device that has just been seen if it is an access
// point. It runs in the background: the adapter reporting the device is in the
// middle of answering it, and adopting calls back into that adapter.
func (r *Registry) maybeAdopt(ctx context.Context, d store.Device) {
	if d.Kind != store.DeviceKindAccessPoint || d.Forgetting {
		return
	}
	if _, busy := r.adopting.LoadOrStore(d.ID, struct{}{}); busy {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		defer r.adopting.Delete(d.ID)
		_, err := core.New(r.store, r).Devices.Adopt(ctx, d.ID, "")
		switch {
		case err == nil:
			slog.InfoContext(ctx, "adopted access point automatically",
				slog.String("device.id", d.ID), slog.String("adapter", d.Adapter), slog.String("model", d.Model))
		case errors.Is(err, store.ErrFailedPrecondition):
			// Adopted by somebody else in the meantime, or locked.
		default:
			slog.WarnContext(ctx, "could not adopt access point automatically; it stays pending",
				slog.String("device.id", d.ID), slog.Any("error", err))
		}
	}()
}

// Wait blocks until the adapters started by Start have returned.
func (r *Registry) Wait() { r.wg.Wait() }

func (r *Registry) setHealth(name string, healthy bool, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.adapters[name]
	if !ok {
		return
	}
	e.status.Healthy = healthy
	e.status.StatusMessage = msg
}

func (r *Registry) ListAdapters(context.Context) []core.AdapterStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]core.AdapterStatus, 0, len(r.adapters))
	for _, e := range r.adapters {
		out = append(out, e.status)
	}
	return out
}

func (r *Registry) AdoptDevice(ctx context.Context, d store.Device) (err error) {
	defer func() { r.countAdoption(ctx, d.Adapter, err) }()
	drv, err := r.driver(d.Adapter)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return drv.Adopt(ctx, toDevice(d))
}

// ForgetDevice tells the device's adapter the controller no longer manages the
// device. deferred is true when the adapter will finish later, by resetting the
// device on its next contact (adapter.ErrForgetDeferred); the device is then
// kept until the adapter reports it released.
func (r *Registry) ForgetDevice(ctx context.Context, d store.Device) (deferred bool, err error) {
	drv, err := r.driver(d.Adapter)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	err = drv.Forget(ctx, toDevice(d))
	if errors.Is(err, adapter.ErrForgetDeferred) {
		return true, nil
	}
	return false, err
}

// driver returns the driver of a connected adapter.
func (r *Registry) driver(name string) (Driver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.adapters[name]
	if !ok || !e.status.Healthy {
		return nil, fmt.Errorf("%w: adapter %q is not connected", store.ErrFailedPrecondition, name)
	}
	return e.driver, nil
}

// --- external adapters ---

// CommandKind is what the controller asks an external adapter to do.
type CommandKind int

const (
	CommandAdopt CommandKind = iota + 1
	CommandForget
)

// Command is sent to an external adapter, which answers it with
// Registry.CompleteCommand.
type Command struct {
	ID     string
	Kind   CommandKind
	Device adapter.Device
	// Trace is the W3C trace context of the request that caused the command.
	Trace map[string]string
}

type pendingCommand struct {
	adapter string
	done    chan error // buffered; receives the adapter's answer
}

// Session is the controller's end of a connected external adapter.
type Session struct {
	reg      *Registry
	name     string
	commands chan Command
	closed   chan struct{}
	once     sync.Once
}

// Connect registers an external adapter that has authenticated as name. It
// fails with ErrConflict if a built-in adapter has that name or an external
// one is already connected under it. Close the session when the adapter's
// stream ends.
func (r *Registry) Connect(name, version string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.adapters[name]
	switch {
	case ok && e.status.Kind == core.AdapterBuiltin:
		return nil, fmt.Errorf("%w: %q is a built-in adapter", store.ErrConflict, name)
	case ok && e.status.Healthy:
		return nil, fmt.Errorf("%w: adapter %q is already connected", store.ErrConflict, name)
	case !ok:
		e = &entry{}
		r.adapters[name] = e
	}
	s := &Session{reg: r, name: name, commands: make(chan Command), closed: make(chan struct{})}
	e.status = core.AdapterStatus{
		Name:        name,
		Kind:        core.AdapterExternal,
		Version:     version,
		Healthy:     true,
		ConnectTime: r.now(),
	}
	e.driver = remoteDriver{s}
	return s, nil
}

// Commands delivers the commands to send to the adapter.
func (s *Session) Commands() <-chan Command { return s.commands }

// Close marks the adapter disconnected and fails its outstanding commands.
func (s *Session) Close() {
	s.once.Do(func() {
		close(s.closed)
		s.reg.mu.Lock()
		defer s.reg.mu.Unlock()
		// A newer session may already have replaced this one.
		if e, ok := s.reg.adapters[s.name]; ok {
			if d, ok := e.driver.(remoteDriver); ok && d.s == s {
				e.status.Healthy = false
				e.status.StatusMessage = "disconnected"
			}
		}
	})
}

// CompleteCommand records an external adapter's answer to a command. An
// empty errMsg means success; deferred means a Forget the adapter will finish
// when the device next contacts it (adapter.ErrForgetDeferred). Only the
// adapter that was sent the command may answer it.
func (r *Registry) CompleteCommand(adapterName, id, errMsg string, deferred bool) error {
	r.mu.Lock()
	p, ok := r.pending[id]
	if !ok || p.adapter != adapterName {
		r.mu.Unlock()
		return fmt.Errorf("%w: no such command", store.ErrNotFound)
	}
	delete(r.pending, id)
	r.mu.Unlock()
	switch {
	case errMsg != "":
		p.done <- errors.New(errMsg)
	case deferred:
		p.done <- adapter.ErrForgetDeferred
	default:
		p.done <- nil
	}
	return nil
}

type remoteDriver struct{ s *Session }

func (d remoteDriver) Adopt(ctx context.Context, dev adapter.Device) error {
	return d.send(ctx, CommandAdopt, dev)
}

func (d remoteDriver) Forget(ctx context.Context, dev adapter.Device) error {
	return d.send(ctx, CommandForget, dev)
}

func (d remoteDriver) send(ctx context.Context, kind CommandKind, dev adapter.Device) error {
	r := d.s.reg
	id := rand.Text()
	var trace map[string]string
	if r.prop != nil {
		carrier := propagation.MapCarrier{}
		r.prop.Inject(ctx, carrier)
		trace = carrier
	}
	p := &pendingCommand{adapter: d.s.name, done: make(chan error, 1)}
	r.mu.Lock()
	r.pending[id] = p
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
	}()

	select {
	case d.s.commands <- Command{ID: id, Kind: kind, Device: dev, Trace: trace}:
	case <-d.s.closed:
		return fmt.Errorf("%w: adapter %q disconnected", store.ErrFailedPrecondition, d.s.name)
	case <-ctx.Done():
		return fmt.Errorf("sending command to adapter %q: %w", d.s.name, ctx.Err())
	}
	select {
	case err := <-p.done:
		return err
	case <-d.s.closed:
		return fmt.Errorf("%w: adapter %q disconnected", store.ErrFailedPrecondition, d.s.name)
	case <-ctx.Done():
		return fmt.Errorf("waiting for adapter %q: %w", d.s.name, ctx.Err())
	}
}

// --- metrics ---

func (r *Registry) registerMetrics(m metric.Meter) error {
	var err error
	if r.adoptions, err = m.Int64Counter("beenfar.adoptions",
		metric.WithDescription("Adoptions handed to an adapter, by adapter and result."),
		metric.WithUnit("{adoption}")); err != nil {
		return err
	}
	devices, err := m.Int64ObservableGauge("beenfar.devices",
		metric.WithDescription("Known devices, by adapter and state."),
		metric.WithUnit("{device}"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		all, err := r.store.ListDevices(ctx, store.DeviceFilter{})
		if err != nil {
			return err
		}
		type key struct {
			adapter string
			state   store.DeviceState
			locked  bool
		}
		counts := map[key]int64{}
		for _, d := range all {
			counts[key{d.Adapter, d.State, d.Locked}]++
		}
		for k, n := range counts {
			o.ObserveInt64(devices, n, metric.WithAttributes(
				attribute.String("adapter", k.adapter),
				attribute.String("state", string(k.state)),
				attribute.Bool("locked", k.locked)))
		}
		return nil
	}, devices)
	return err
}

func (r *Registry) countAdoption(ctx context.Context, adapterName string, err error) {
	if r.adoptions == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	r.adoptions.Add(ctx, 1, metric.WithAttributes(attribute.String("adapter", adapterName), attribute.String("result", result)))
}
