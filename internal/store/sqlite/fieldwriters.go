package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/sqlite/gen/db"
)

func (s *Store) RecordFieldWriters(ctx context.Context, resource store.FieldResource, id string, fields []string, w store.FieldWriter) error {
	if w.Writer == "" {
		return fmt.Errorf("%w: field writer needs a name", store.ErrInvalid)
	}
	return s.inTx(ctx, func(txs *Store) error {
		now := txs.nowMilli()
		for _, f := range fields {
			if err := txs.q.UpsertFieldWriter(ctx, db.UpsertFieldWriterParams{
				Resource:   string(resource),
				ResourceID: id,
				Field:      f,
				Writer:     w.Writer,
				Kind:       string(w.Kind),
				WriteTime:  now,
			}); err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
}

func (s *Store) GetFieldWriters(ctx context.Context, resource store.FieldResource, id string) ([]store.FieldWriter, error) {
	rows, err := s.q.ListFieldWritersOf(ctx, db.ListFieldWritersOfParams{Resource: string(resource), ResourceID: id})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]store.FieldWriter, 0, len(rows))
	for _, r := range rows {
		out = append(out, fieldWriter(r.Field, r.Writer, r.Kind, r.WriteTime))
	}
	return out, nil
}

func (s *Store) ListFieldWriters(ctx context.Context, resource store.FieldResource) (map[string][]store.FieldWriter, error) {
	rows, err := s.q.ListFieldWriters(ctx, string(resource))
	if err != nil {
		return nil, mapErr(err)
	}
	out := map[string][]store.FieldWriter{}
	for _, r := range rows {
		out[r.ResourceID] = append(out[r.ResourceID], fieldWriter(r.Field, r.Writer, r.Kind, r.WriteTime))
	}
	return out, nil
}

func fieldWriter(field, writer, kind string, writeTime int64) store.FieldWriter {
	return store.FieldWriter{Field: field, Writer: writer, Kind: store.FieldWriterKind(kind), WriteTime: time.UnixMilli(writeTime).UTC()}
}
