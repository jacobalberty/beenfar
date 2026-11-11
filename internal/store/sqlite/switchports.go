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

func toSwitchPort(p db.SwitchPort, tagged []string) store.SwitchPort {
	out := dbconvgen.SwitchPort(p)
	out.TaggedNetworkIDs = tagged
	if out.TaggedNetworkIDs == nil {
		out.TaggedNetworkIDs = []string{}
	}
	return out
}

// checkPortRefs returns ErrInvalid if the port refers to a network that does
// not exist.
func (txs *Store) checkPortRefs(ctx context.Context, p store.SwitchPort) error {
	if err := txs.checkNetworkRef(ctx, p.NativeNetworkID); err != nil {
		return err
	}
	for _, id := range p.TaggedNetworkIDs {
		if id == "" {
			return fmt.Errorf("%w: tagged network ids must not be empty", store.ErrInvalid)
		}
		if err := txs.checkNetworkRef(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (txs *Store) replaceTaggedNetworks(ctx context.Context, id string, networks []string) ([]string, error) {
	if err := txs.q.DeleteTaggedNetworks(ctx, id); err != nil {
		return nil, mapErr(err)
	}
	networks = normalizeGroups(networks)
	for _, n := range networks {
		if err := txs.q.InsertTaggedNetwork(ctx, db.InsertTaggedNetworkParams{SwitchPortID: id, NetworkID: n}); err != nil {
			return nil, mapErr(err)
		}
	}
	return networks, nil
}

func (s *Store) CreateSwitchPort(ctx context.Context, p store.SwitchPort) (store.SwitchPort, error) {
	var out store.SwitchPort
	err := s.inTx(ctx, func(txs *Store) error {
		// The foreign key would report a missing device without saying which.
		if _, err := txs.q.GetDevice(ctx, p.DeviceID); err != nil {
			if err := mapErr(err); !errors.Is(err, store.ErrNotFound) {
				return err
			}
			return fmt.Errorf("%w: device %q does not exist", store.ErrInvalid, p.DeviceID)
		}
		if err := txs.checkPortRefs(ctx, p); err != nil {
			return err
		}
		row, err := txs.q.CreateSwitchPort(ctx, db.CreateSwitchPortParams{
			ID:              newID(),
			DeviceID:        p.DeviceID,
			PortIndex:       int64(p.PortIndex),
			Name:            p.Name,
			Disabled:        p.Disabled,
			NativeNetworkID: p.NativeNetworkID,
			Tagging:         string(p.Tagging),
			PoeMode:         string(p.PoEMode),
			Now:             txs.nowMilli(),
		})
		if err != nil {
			return mapErr(err)
		}
		tagged, err := txs.replaceTaggedNetworks(ctx, row.ID, p.TaggedNetworkIDs)
		if err != nil {
			return err
		}
		out = toSwitchPort(row, tagged)
		return nil
	})
	return out, err
}

func (s *Store) GetSwitchPort(ctx context.Context, id string) (store.SwitchPort, error) {
	row, err := s.q.GetSwitchPort(ctx, id)
	if err != nil {
		return store.SwitchPort{}, mapErr(err)
	}
	tagged, err := s.q.ListTaggedNetworks(ctx, id)
	if err != nil {
		return store.SwitchPort{}, mapErr(err)
	}
	return toSwitchPort(row, tagged), nil
}

func (s *Store) ListSwitchPorts(ctx context.Context, deviceID string) ([]store.SwitchPort, error) {
	rows, err := s.q.ListSwitchPorts(ctx, deviceID)
	if err != nil {
		return nil, mapErr(err)
	}
	all, err := s.q.ListAllTaggedNetworks(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	tagged := make(map[string][]string)
	for _, t := range all {
		tagged[t.SwitchPortID] = append(tagged[t.SwitchPortID], t.NetworkID)
	}
	out := make([]store.SwitchPort, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSwitchPort(r, tagged[r.ID]))
	}
	return out, nil
}

func (s *Store) UpdateSwitchPort(ctx context.Context, p store.SwitchPort) (store.SwitchPort, error) {
	var out store.SwitchPort
	err := s.inTx(ctx, func(txs *Store) error {
		if err := txs.checkPortRefs(ctx, p); err != nil {
			return err
		}
		row, err := txs.q.UpdateSwitchPort(ctx, db.UpdateSwitchPortParams{
			ID:               p.ID,
			Name:             p.Name,
			Disabled:         p.Disabled,
			NativeNetworkID:  p.NativeNetworkID,
			Tagging:          string(p.Tagging),
			PoeMode:          string(p.PoEMode),
			Now:              txs.nowMilli(),
			ExpectedRevision: p.Revision,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return txs.revisionMiss(ctx, "switch port", p.ID, txs.switchPortExists)
		}
		if err != nil {
			return mapErr(err)
		}
		tagged, err := txs.replaceTaggedNetworks(ctx, row.ID, p.TaggedNetworkIDs)
		if err != nil {
			return err
		}
		out = toSwitchPort(row, tagged)
		return nil
	})
	return out, err
}

func (s *Store) DeleteSwitchPort(ctx context.Context, id string, revision int64) error {
	return s.inTx(ctx, func(txs *Store) error {
		n, err := txs.q.DeleteSwitchPort(ctx, db.DeleteSwitchPortParams{ID: id, ExpectedRevision: revision})
		if err != nil {
			return mapErr(err)
		}
		if n == 0 {
			return txs.revisionMiss(ctx, "switch port", id, txs.switchPortExists)
		}
		return nil
	})
}

func (txs *Store) switchPortExists(ctx context.Context, id string) error {
	_, err := txs.q.GetSwitchPort(ctx, id)
	return err
}
