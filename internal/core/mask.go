package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/jacobalberty/beenfar/internal/store"
)

// fieldSetters maps the API name of each mutable field of T to a function
// that copies that field from src to dst. It drives partial updates
// (AIP-134 update masks): a client sends the fields it manages and every
// other field keeps its stored value.
type fieldSetters[T any] map[string]func(dst *T, src T)

// apply copies the fields named by paths from src into dst. An empty mask,
// or the single path "*", copies every field. A path that names no mutable
// field is an error, so a typo cannot silently turn into a no-op.
func (f fieldSetters[T]) apply(dst *T, src T, paths []string) error {
	if fullReplace(paths) {
		for _, set := range f {
			set(dst, src)
		}
		return nil
	}
	for _, p := range paths {
		set, ok := f[p]
		if !ok {
			return invalid("update_mask: %q is not an updatable field (updatable: %v)", p, f.names())
		}
		set(dst, src)
	}
	return nil
}

func (f fieldSetters[T]) names() []string {
	out := make([]string, 0, len(f))
	for n := range f {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func fullReplace(paths []string) bool {
	return len(paths) == 0 || (len(paths) == 1 && paths[0] == "*")
}

// updateMasked runs a read-merge-write inside one transaction:
//
//   - get loads the stored record,
//   - a non-zero revision in update must equal the stored one (the etag
//     check, over the whole resource),
//   - the masked fields of update are copied onto the stored record,
//   - normalize validates the merged record, and put writes it, guarded by
//     the stored revision so a concurrent writer cannot be overwritten,
//   - finish runs in the same transaction on the written record and the
//     update mask (it records who wrote the fields).
func updateMasked[T any](
	ctx context.Context,
	st store.Store,
	update T,
	paths []string,
	setters fieldSetters[T],
	revision func(*T) *int64,
	get func(context.Context, store.Store) (T, error),
	normalize func(T) (T, error),
	put func(context.Context, store.Store, T) (T, error),
	finish func(ctx context.Context, tx store.Store, written *T, paths []string) error,
) (T, error) {
	var out T
	err := st.WithTx(ctx, func(tx store.Store) error {
		cur, err := get(ctx, tx)
		if err != nil {
			return err
		}
		stored := *revision(&cur)
		if want := *revision(&update); want != 0 && want != stored {
			return fmt.Errorf("%w: the resource changed since etag %d was read", store.ErrStale, want)
		}
		if err := setters.apply(&cur, update, paths); err != nil {
			return err
		}
		*revision(&cur) = stored
		merged, err := normalize(cur)
		if err != nil {
			return err
		}
		if out, err = put(ctx, tx, merged); err != nil {
			return err
		}
		return finish(ctx, tx, &out, paths)
	})
	return out, err
}
