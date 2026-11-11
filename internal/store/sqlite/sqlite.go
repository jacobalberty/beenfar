// Package sqlite implements store.Store on SQLite using the pure-Go
// modernc.org/sqlite driver, sqlc-generated queries and goose migrations.
package sqlite

// gen/ holds only sqlc output and is wiped first (see api/generate.go).
//go:generate rm -rf gen
//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/jacobalberty/beenfar/internal/store"
	dbconvgen "github.com/jacobalberty/beenfar/internal/store/sqlite/dbconv/gen"
	"github.com/jacobalberty/beenfar/internal/store/sqlite/gen/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is a SQLite-backed store.Store.
type Store struct {
	db  *sql.DB // nil when bound to a transaction
	q   *db.Queries
	now func() time.Time
}

var _ store.Store = (*Store)(nil)

// Open opens (creating if needed) the database file at path and applies all
// pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty sqlite path", store.ErrInvalid)
	}
	dsn := "file:" + path +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_txlock=immediate"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &Store{db: sqlDB, q: db.New(sqlDB), now: time.Now}, nil
}

func migrate(ctx context.Context, sqlDB *sql.DB) error {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB, fsys)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	return s.db.PingContext(ctx)
}

func (s *Store) WithTx(ctx context.Context, fn func(store.Store) error) error {
	return s.inTx(ctx, func(txs *Store) error { return fn(txs) })
}

// inTx runs fn in a transaction, reusing the current one if s is already
// transaction-bound.
func (s *Store) inTx(ctx context.Context, fn func(*Store) error) (err error) {
	if s.db == nil {
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mapErr(err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(&Store{q: s.q.WithTx(tx), now: s.now}); err != nil {
		return err
	}
	return mapErr(tx.Commit())
}

func newID() string { return strings.ToLower(rand.Text()) }

func (s *Store) nowMilli() int64 { return s.now().UnixMilli() }

// toOptMilli maps the zero time to 0 (unset); dbconv.FromOptMilli reverses it.
func toOptMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// mapErr translates driver errors into store errors.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if se, ok := errors.AsType[*sqlite.Error](err); ok {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%w: %s", store.ErrConflict, uniqueMessage(err))
		case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_NOTNULL, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return fmt.Errorf("%w: %v", store.ErrInvalid, err)
		}
	}
	return err
}

// uniqueMessage turns "UNIQUE constraint failed: wifi_networks.ssid (2067)"
// into "ssid already in use", which is safe to show API clients.
func uniqueMessage(err error) string {
	_, cols, ok := strings.Cut(err.Error(), "constraint failed: ")
	if !ok {
		return "already exists"
	}
	cols, _, _ = strings.Cut(cols, " (")
	var names []string
	for c := range strings.SplitSeq(cols, ",") {
		c = strings.TrimSpace(c)
		if _, col, ok := strings.Cut(c, "."); ok {
			c = col
		}
		names = append(names, c)
	}
	return strings.Join(names, ", ") + " already in use"
}

// --- devices ---

func (s *Store) ReportDevice(ctx context.Context, d store.DiscoveredDevice) (store.Device, error) {
	mac, err := store.NormalizeMAC(d.MAC)
	if err != nil {
		return store.Device{}, err
	}
	if d.Adapter == "" {
		return store.Device{}, fmt.Errorf("%w: adapter is required", store.ErrInvalid)
	}
	row, err := s.q.ReportDevice(ctx, db.ReportDeviceParams{
		ID:       newID(),
		Mac:      mac,
		Adapter:  d.Adapter,
		Model:    d.Model,
		Ip:       d.IP,
		Firmware: d.Firmware,
		Now:      s.nowMilli(),
	})
	if err != nil {
		return store.Device{}, mapErr(err)
	}
	return dbconvgen.Device(row), nil
}

func (s *Store) GetDevice(ctx context.Context, id string) (store.Device, error) {
	row, err := s.q.GetDevice(ctx, id)
	if err != nil {
		return store.Device{}, mapErr(err)
	}
	return dbconvgen.Device(row), nil
}

func (s *Store) GetDeviceByMAC(ctx context.Context, mac string) (store.Device, error) {
	mac, err := store.NormalizeMAC(mac)
	if err != nil {
		return store.Device{}, err
	}
	row, err := s.q.GetDeviceByMAC(ctx, mac)
	if err != nil {
		return store.Device{}, mapErr(err)
	}
	return dbconvgen.Device(row), nil
}

func (s *Store) ListDevices(ctx context.Context, f store.DeviceFilter) ([]store.Device, error) {
	rows, err := s.q.ListDevices(ctx, db.ListDevicesParams{State: string(f.State), Adapter: f.Adapter})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]store.Device, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbconvgen.Device(r))
	}
	return out, nil
}

func (s *Store) AdoptDevice(ctx context.Context, id string) (store.Device, error) {
	var out store.Device
	err := s.inTx(ctx, func(txs *Store) error {
		row, err := txs.q.AdoptDevice(ctx, db.AdoptDeviceParams{ID: id, Now: txs.nowMilli()})
		if err == nil {
			out = dbconvgen.Device(row)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return mapErr(err)
		}
		// Distinguish "no such device" from "already adopted".
		if _, err := txs.q.GetDevice(ctx, id); err != nil {
			return mapErr(err)
		}
		return fmt.Errorf("%w: device %s is already adopted", store.ErrFailedPrecondition, id)
	})
	return out, err
}

func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	n, err := s.q.DeleteDevice(ctx, id)
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- wifi networks ---

func toWifiNetwork(w db.WifiNetwork, groups []string) store.WifiNetwork {
	out := dbconvgen.WifiNetwork(w)
	out.APGroups = groups
	if out.APGroups == nil {
		out.APGroups = []string{}
	}
	return out
}

func validateWifi(w store.WifiNetwork) error {
	if w.SSID == "" || len(w.SSID) > 32 {
		return fmt.Errorf("%w: ssid must be 1-32 bytes", store.ErrInvalid)
	}
	return nil
}

func normalizeGroups(groups []string) []string {
	out := slices.Clone(groups)
	slices.Sort(out)
	return slices.Compact(out)
}

func (txs *Store) replaceAPGroups(ctx context.Context, id string, groups []string) ([]string, error) {
	if err := txs.q.DeleteAPGroups(ctx, id); err != nil {
		return nil, mapErr(err)
	}
	groups = normalizeGroups(groups)
	for _, g := range groups {
		if err := txs.q.InsertAPGroup(ctx, db.InsertAPGroupParams{WifiNetworkID: id, ApGroup: g}); err != nil {
			return nil, mapErr(err)
		}
	}
	return groups, nil
}

func (s *Store) CreateWifiNetwork(ctx context.Context, w store.WifiNetwork) (store.WifiNetwork, error) {
	if err := validateWifi(w); err != nil {
		return store.WifiNetwork{}, err
	}
	var out store.WifiNetwork
	err := s.inTx(ctx, func(txs *Store) error {
		if err := txs.checkNetworkRef(ctx, w.NetworkID); err != nil {
			return err
		}
		row, err := txs.q.CreateWifiNetwork(ctx, db.CreateWifiNetworkParams{
			ID:               newID(),
			Ssid:             w.SSID,
			Security:         string(w.Security),
			SecurityKey:      w.SecurityKey,
			Band:             string(w.Band),
			NetworkID:        w.NetworkID,
			Guest:            w.Guest,
			Hidden:           w.Hidden,
			DefaultUserGroup: w.DefaultUserGroup,
			RadiusProfile:    w.RadiusProfile,
			Now:              txs.nowMilli(),
		})
		if err != nil {
			return mapErr(err)
		}
		groups, err := txs.replaceAPGroups(ctx, row.ID, w.APGroups)
		if err != nil {
			return err
		}
		out = toWifiNetwork(row, groups)
		return nil
	})
	return out, err
}

func (s *Store) GetWifiNetwork(ctx context.Context, id string) (store.WifiNetwork, error) {
	row, err := s.q.GetWifiNetwork(ctx, id)
	if err != nil {
		return store.WifiNetwork{}, mapErr(err)
	}
	groups, err := s.q.ListAPGroups(ctx, id)
	if err != nil {
		return store.WifiNetwork{}, mapErr(err)
	}
	return toWifiNetwork(row, groups), nil
}

func (s *Store) ListWifiNetworks(ctx context.Context) ([]store.WifiNetwork, error) {
	rows, err := s.q.ListWifiNetworks(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	all, err := s.q.ListAllAPGroups(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	groups := make(map[string][]string)
	for _, g := range all {
		groups[g.WifiNetworkID] = append(groups[g.WifiNetworkID], g.ApGroup)
	}
	out := make([]store.WifiNetwork, 0, len(rows))
	for _, r := range rows {
		out = append(out, toWifiNetwork(r, groups[r.ID]))
	}
	return out, nil
}

func (s *Store) UpdateWifiNetwork(ctx context.Context, w store.WifiNetwork) (store.WifiNetwork, error) {
	if err := validateWifi(w); err != nil {
		return store.WifiNetwork{}, err
	}
	var out store.WifiNetwork
	err := s.inTx(ctx, func(txs *Store) error {
		if err := txs.checkNetworkRef(ctx, w.NetworkID); err != nil {
			return err
		}
		row, err := txs.q.UpdateWifiNetwork(ctx, db.UpdateWifiNetworkParams{
			ID:               w.ID,
			Ssid:             w.SSID,
			Security:         string(w.Security),
			SecurityKey:      w.SecurityKey,
			Band:             string(w.Band),
			NetworkID:        w.NetworkID,
			Guest:            w.Guest,
			Hidden:           w.Hidden,
			DefaultUserGroup: w.DefaultUserGroup,
			RadiusProfile:    w.RadiusProfile,
			Now:              txs.nowMilli(),
			ExpectedRevision: w.Revision,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return txs.revisionMiss(ctx, "wifi network", w.ID, txs.wifiExists)
		}
		if err != nil {
			return mapErr(err)
		}
		groups, err := txs.replaceAPGroups(ctx, row.ID, w.APGroups)
		if err != nil {
			return err
		}
		out = toWifiNetwork(row, groups)
		return nil
	})
	return out, err
}

func (s *Store) DeleteWifiNetwork(ctx context.Context, id string, revision int64) error {
	return s.inTx(ctx, func(txs *Store) error {
		n, err := txs.q.DeleteWifiNetwork(ctx, db.DeleteWifiNetworkParams{ID: id, ExpectedRevision: revision})
		if err != nil {
			return mapErr(err)
		}
		if n == 0 {
			return txs.revisionMiss(ctx, "wifi network", id, txs.wifiExists)
		}
		return nil
	})
}

// checkNetworkRef returns ErrInvalid if id is set but names no network.
func (txs *Store) checkNetworkRef(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	err := mapErr(txs.networkExists(ctx, id))
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: network %q does not exist", store.ErrInvalid, id)
	}
	return err
}

func (txs *Store) wifiExists(ctx context.Context, id string) error {
	_, err := txs.q.GetWifiNetwork(ctx, id)
	return err
}

// revisionMiss explains why a conditional write by id matched no row: either
// the record does not exist, or its revision changed.
func (txs *Store) revisionMiss(ctx context.Context, kind, id string, exists func(context.Context, string) error) error {
	if err := exists(ctx, id); err != nil {
		return mapErr(err)
	}
	return fmt.Errorf("%w: %s %s was modified", store.ErrStale, kind, id)
}
