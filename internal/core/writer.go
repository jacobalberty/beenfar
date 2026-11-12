package core

import (
	"context"

	"github.com/jacobalberty/beenfar/internal/store"
)

type writerKey struct{}

// WithWriter returns a context in which creates and updates are recorded as
// written by w (only Writer and Kind matter). The server sets it from the
// authenticated caller, so core does not depend on package auth. Without it
// writes are not attributed.
func WithWriter(ctx context.Context, w store.FieldWriter) context.Context {
	return context.WithValue(ctx, writerKey{}, w)
}

func writerFrom(ctx context.Context) (store.FieldWriter, bool) {
	w, ok := ctx.Value(writerKey{}).(store.FieldWriter)
	return w, ok && w.Writer != ""
}

// tracking records and reports the last writer of each field of one kind of
// resource T. Its writes run in the caller's transaction.
type tracking[T any] struct {
	resource store.FieldResource
	fields   fieldSetters[T]
	id       func(*T) string
	set      func(*T, []store.FieldWriter)
}

// afterWrite attributes the fields a create or update touched to the
// context's writer, and fills v.FieldWriters. paths are the fields named by
// the update mask; an empty mask, "*" and creates touch every field.
func (t tracking[T]) afterWrite(ctx context.Context, st store.Store, v *T, paths []string) error {
	if w, ok := writerFrom(ctx); ok {
		fields := paths
		if fullReplace(paths) {
			fields = t.fields.names()
		}
		if err := st.RecordFieldWriters(ctx, t.resource, t.id(v), fields, w); err != nil {
			return err
		}
	}
	return t.load(ctx, st, v)
}

// load fills v.FieldWriters.
func (t tracking[T]) load(ctx context.Context, st store.Store, v *T) error {
	ws, err := st.GetFieldWriters(ctx, t.resource, t.id(v))
	if err != nil {
		return err
	}
	t.set(v, ws)
	return nil
}

// loadAll fills the FieldWriters of every element of vs with one query.
func (t tracking[T]) loadAll(ctx context.Context, st store.Store, vs []T) error {
	all, err := st.ListFieldWriters(ctx, t.resource)
	if err != nil {
		return err
	}
	for i := range vs {
		t.set(&vs[i], all[t.id(&vs[i])])
	}
	return nil
}

// create runs fn (which creates the resource) and attributes the result in
// the same transaction.
func (t tracking[T]) create(ctx context.Context, st store.Store, fn func(store.Store) (T, error)) (T, error) {
	var out T
	err := st.WithTx(ctx, func(tx store.Store) error {
		var err error
		if out, err = fn(tx); err != nil {
			return err
		}
		return t.afterWrite(ctx, tx, &out, nil)
	})
	return out, err
}

var (
	wifiTracking = tracking[store.WifiNetwork]{
		resource: store.FieldResourceWifiNetwork, fields: wifiFields,
		id:  func(w *store.WifiNetwork) string { return w.ID },
		set: func(w *store.WifiNetwork, ws []store.FieldWriter) { w.FieldWriters = ws },
	}
	networkTracking = tracking[store.Network]{
		resource: store.FieldResourceNetwork, fields: networkFields,
		id:  func(n *store.Network) string { return n.ID },
		set: func(n *store.Network, ws []store.FieldWriter) { n.FieldWriters = ws },
	}
	radiusTracking = tracking[store.RadiusServer]{
		resource: store.FieldResourceRadius, fields: radiusFields,
		id:  func(r *store.RadiusServer) string { return r.ID },
		set: func(r *store.RadiusServer, ws []store.FieldWriter) { r.FieldWriters = ws },
	}
	switchPortTracking = tracking[store.SwitchPort]{
		resource: store.FieldResourceSwitchPort, fields: switchPortFields,
		id:  func(p *store.SwitchPort) string { return p.ID },
		set: func(p *store.SwitchPort, ws []store.FieldWriter) { p.FieldWriters = ws },
	}
)
