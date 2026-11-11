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

func validateNetwork(n store.Network) error {
	if n.Name == "" || len(n.Name) > 64 {
		return fmt.Errorf("%w: name must be 1-64 bytes", store.ErrInvalid)
	}
	return nil
}

func (s *Store) CreateNetwork(ctx context.Context, n store.Network) (store.Network, error) {
	if err := validateNetwork(n); err != nil {
		return store.Network{}, err
	}
	row, err := s.q.CreateNetwork(ctx, db.CreateNetworkParams{
		ID:          newID(),
		Name:        n.Name,
		VlanID:      int64(n.VLANID),
		Subnet:      n.Subnet,
		DhcpEnabled: n.DHCPEnabled,
		DhcpStart:   n.DHCPStart,
		DhcpStop:    n.DHCPStop,
		DomainName:  n.DomainName,
		Now:         s.nowMilli(),
	})
	if err != nil {
		return store.Network{}, mapErr(err)
	}
	return dbconvgen.Network(row), nil
}

func (s *Store) GetNetwork(ctx context.Context, id string) (store.Network, error) {
	row, err := s.q.GetNetwork(ctx, id)
	if err != nil {
		return store.Network{}, mapErr(err)
	}
	return dbconvgen.Network(row), nil
}

func (s *Store) ListNetworks(ctx context.Context) ([]store.Network, error) {
	rows, err := s.q.ListNetworks(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]store.Network, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbconvgen.Network(r))
	}
	return out, nil
}

func (s *Store) UpdateNetwork(ctx context.Context, n store.Network) (store.Network, error) {
	if err := validateNetwork(n); err != nil {
		return store.Network{}, err
	}
	var out store.Network
	err := s.inTx(ctx, func(txs *Store) error {
		row, err := txs.q.UpdateNetwork(ctx, db.UpdateNetworkParams{
			ID:               n.ID,
			Name:             n.Name,
			VlanID:           int64(n.VLANID),
			Subnet:           n.Subnet,
			DhcpEnabled:      n.DHCPEnabled,
			DhcpStart:        n.DHCPStart,
			DhcpStop:         n.DHCPStop,
			DomainName:       n.DomainName,
			Now:              txs.nowMilli(),
			ExpectedRevision: n.Revision,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return txs.revisionMiss(ctx, "network", n.ID, txs.networkExists)
		}
		if err != nil {
			return mapErr(err)
		}
		out = dbconvgen.Network(row)
		return nil
	})
	return out, err
}

func (s *Store) DeleteNetwork(ctx context.Context, id string, revision int64) error {
	return s.inTx(ctx, func(txs *Store) error {
		users, err := txs.q.CountWifiNetworksUsingNetwork(ctx, id)
		if err != nil {
			return mapErr(err)
		}
		if users > 0 {
			if err := txs.networkExists(ctx, id); err != nil {
				return mapErr(err)
			}
			return fmt.Errorf("%w: network %s is used by %d wireless network(s)", store.ErrFailedPrecondition, id, users)
		}
		n, err := txs.q.DeleteNetwork(ctx, db.DeleteNetworkParams{ID: id, ExpectedRevision: revision})
		if err != nil {
			return mapErr(err)
		}
		if n == 0 {
			return txs.revisionMiss(ctx, "network", id, txs.networkExists)
		}
		return nil
	})
}

func (txs *Store) networkExists(ctx context.Context, id string) error {
	_, err := txs.q.GetNetwork(ctx, id)
	return err
}
