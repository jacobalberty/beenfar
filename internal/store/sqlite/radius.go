package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jacobalberty/beenfar/internal/store"
	dbconvgen "github.com/jacobalberty/beenfar/internal/store/sqlite/dbconv/gen"
	"github.com/jacobalberty/beenfar/internal/store/sqlite/gen/db"
)

func validateRadius(r store.RadiusServer) error {
	if r.Name == "" || len(r.Name) > 64 {
		return fmt.Errorf("%w: name must be 1-64 bytes", store.ErrInvalid)
	}
	if r.SharedSecret == "" {
		return fmt.Errorf("%w: shared_secret is required", store.ErrInvalid)
	}
	return nil
}

func (s *Store) CreateRadiusServer(ctx context.Context, r store.RadiusServer) (store.RadiusServer, error) {
	if err := validateRadius(r); err != nil {
		return store.RadiusServer{}, err
	}
	row, err := s.q.CreateRadiusServer(ctx, db.CreateRadiusServerParams{
		ID:             newID(),
		Name:           r.Name,
		Address:        r.Address,
		AuthPort:       int64(r.AuthPort),
		AccountingPort: int64(r.AccountingPort),
		SharedSecret:   r.SharedSecret,
		Now:            s.nowMilli(),
	})
	if err != nil {
		return store.RadiusServer{}, mapErr(err)
	}
	return dbconvgen.RadiusServer(row), nil
}

func (s *Store) GetRadiusServer(ctx context.Context, id string) (store.RadiusServer, error) {
	row, err := s.q.GetRadiusServer(ctx, id)
	if err != nil {
		return store.RadiusServer{}, mapErr(err)
	}
	return dbconvgen.RadiusServer(row), nil
}

func (s *Store) ListRadiusServers(ctx context.Context) ([]store.RadiusServer, error) {
	rows, err := s.q.ListRadiusServers(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	return dbconvgen.RadiusServers(rows), nil
}

func (s *Store) UpdateRadiusServer(ctx context.Context, r store.RadiusServer) (store.RadiusServer, error) {
	if err := validateRadius(r); err != nil {
		return store.RadiusServer{}, err
	}
	var out store.RadiusServer
	err := s.inTx(ctx, func(txs *Store) error {
		row, err := txs.q.UpdateRadiusServer(ctx, db.UpdateRadiusServerParams{
			ID:               r.ID,
			Name:             r.Name,
			Address:          r.Address,
			AuthPort:         int64(r.AuthPort),
			AccountingPort:   int64(r.AccountingPort),
			SharedSecret:     r.SharedSecret,
			Now:              txs.nowMilli(),
			ExpectedRevision: r.Revision,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return txs.revisionMiss(ctx, "radius server", r.ID, txs.radiusExists)
		}
		if err != nil {
			return mapErr(err)
		}
		out = dbconvgen.RadiusServer(row)
		return nil
	})
	return out, err
}

func (s *Store) DeleteRadiusServer(ctx context.Context, id string, revision int64) error {
	return s.inTx(ctx, func(txs *Store) error {
		users, err := txs.q.CountWifiNetworksUsingRadiusServer(ctx, id)
		if err != nil {
			return mapErr(err)
		}
		if users > 0 {
			if err := txs.radiusExists(ctx, id); err != nil {
				return mapErr(err)
			}
			return fmt.Errorf("%w: radius server %s is used by %d wireless network(s)", store.ErrFailedPrecondition, id, users)
		}
		n, err := txs.q.DeleteRadiusServer(ctx, db.DeleteRadiusServerParams{ID: id, ExpectedRevision: revision})
		if err != nil {
			return mapErr(err)
		}
		if n == 0 {
			return txs.revisionMiss(ctx, "radius server", id, txs.radiusExists)
		}
		return nil
	})
}

func (txs *Store) radiusExists(ctx context.Context, id string) error {
	_, err := txs.q.GetRadiusServer(ctx, id)
	return err
}

// checkRadiusRef returns ErrInvalid if id is set but names no RADIUS server.
func (txs *Store) checkRadiusRef(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	err := mapErr(txs.radiusExists(ctx, id))
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: radius server %q does not exist", store.ErrInvalid, id)
	}
	return err
}
