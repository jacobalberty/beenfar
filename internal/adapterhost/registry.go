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

	wg       sync.WaitGroup
	mu       sync.RWMutex
	adapters map[string]*entry
	handlers map[string]http.Handler
	pending  map[string]*pendingCommand // by command id
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
}

// NewRegistry returns an empty registry.
func NewRegistry(opts Options) (*Registry, error) {
	r := &Registry{
		store:    opts.Store,
		prop:     opts.Propagator,
		now:      time.Now,
		adapters: map[string]*entry{},
		handlers: map[string]http.Handler{},
		pending:  map[string]*pendingCommand{},
	}
	if opts.Meter != nil {
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
			err := a.Start(ctx, NewHost(name, r.store))
			if err != nil && !errors.Is(err, context.Canceled) {
				slog.ErrorContext(ctx, "adapter stopped", slog.String("adapter", name), slog.Any("error", err))
				r.setHealth(name, false, err.Error())
				return
			}
			r.setHealth(name, false, "")
		})
	}
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

func (r *Registry) AdoptDevice(ctx context.Context, d store.Device) error {
	drv, err := r.driver(d.Adapter)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return drv.Adopt(ctx, toDevice(d))
}

func (r *Registry) ForgetDevice(ctx context.Context, d store.Device) error {
	drv, err := r.driver(d.Adapter)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return drv.Forget(ctx, toDevice(d))
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
// empty errMsg means success. Only the adapter that was sent the command may
// answer it.
func (r *Registry) CompleteCommand(adapterName, id, errMsg string) error {
	r.mu.Lock()
	p, ok := r.pending[id]
	if !ok || p.adapter != adapterName {
		r.mu.Unlock()
		return fmt.Errorf("%w: no such command", store.ErrNotFound)
	}
	delete(r.pending, id)
	r.mu.Unlock()
	if errMsg != "" {
		p.done <- errors.New(errMsg)
	} else {
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
