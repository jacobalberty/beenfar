package core

import (
	"context"
	"net/netip"

	"github.com/jacobalberty/beenfar/internal/store"
)

// DefaultRadiusAuthPort is used when a RADIUS server is given no
// authentication port.
const DefaultRadiusAuthPort = 1812

// RadiusService manages RADIUS servers.
type RadiusService struct {
	store store.Store
}

func (s *RadiusService) Create(ctx context.Context, r store.RadiusServer) (store.RadiusServer, error) {
	if r.SharedSecret == "" {
		return store.RadiusServer{}, invalid("shared_secret is required")
	}
	r, err := normalizeRadius(r)
	if err != nil {
		return store.RadiusServer{}, err
	}
	return s.store.CreateRadiusServer(ctx, r)
}

func (s *RadiusService) Get(ctx context.Context, id string) (store.RadiusServer, error) {
	return s.store.GetRadiusServer(ctx, id)
}

func (s *RadiusService) List(ctx context.Context) ([]store.RadiusServer, error) {
	return s.store.ListRadiusServers(ctx)
}

// radiusFields are the mutable fields of a RADIUS server, by API name. The
// API never returns the shared secret, so a client cannot send it back
// unchanged: an empty secret keeps the stored one, whether or not the mask
// names it.
var radiusFields = fieldSetters[store.RadiusServer]{
	"name":            func(d *store.RadiusServer, s store.RadiusServer) { d.Name = s.Name },
	"address":         func(d *store.RadiusServer, s store.RadiusServer) { d.Address = s.Address },
	"auth_port":       func(d *store.RadiusServer, s store.RadiusServer) { d.AuthPort = s.AuthPort },
	"accounting_port": func(d *store.RadiusServer, s store.RadiusServer) { d.AccountingPort = s.AccountingPort },
	"shared_secret": func(d *store.RadiusServer, s store.RadiusServer) {
		if s.SharedSecret != "" {
			d.SharedSecret = s.SharedSecret
		}
	},
}

// Update replaces the fields named by paths, or every mutable field when
// paths is empty or "*", like WifiService.Update.
func (s *RadiusService) Update(ctx context.Context, r store.RadiusServer, paths []string) (store.RadiusServer, error) {
	return updateMasked(ctx, s.store, r, paths, radiusFields,
		func(r *store.RadiusServer) *int64 { return &r.Revision },
		func(ctx context.Context, tx store.Store) (store.RadiusServer, error) {
			return tx.GetRadiusServer(ctx, r.ID)
		},
		normalizeRadius,
		func(ctx context.Context, tx store.Store, r store.RadiusServer) (store.RadiusServer, error) {
			return tx.UpdateRadiusServer(ctx, r)
		})
}

// Delete deletes by id. A non-zero revision must match. It fails while a
// wireless network refers to the server.
func (s *RadiusService) Delete(ctx context.Context, id string, revision int64) error {
	return s.store.DeleteRadiusServer(ctx, id, revision)
}

func normalizeRadius(r store.RadiusServer) (store.RadiusServer, error) {
	if r.Name == "" || len(r.Name) > 64 {
		return r, invalid("name must be 1-64 bytes")
	}
	a, err := netip.ParseAddr(r.Address)
	if err != nil || a.Zone() != "" {
		return r, invalid("address must be an IPv4 or IPv6 address")
	}
	r.Address = a.Unmap().String()
	if r.AuthPort == 0 {
		r.AuthPort = DefaultRadiusAuthPort
	}
	if r.AuthPort < 1 || r.AuthPort > 65535 {
		return r, invalid("auth_port must be 1-65535")
	}
	if r.AccountingPort < 0 || r.AccountingPort > 65535 {
		return r, invalid("accounting_port must be 0 (no accounting) or 1-65535")
	}
	if len(r.SharedSecret) > 128 || !isPrintableASCII(r.SharedSecret) {
		return r, invalid("shared_secret must be 1-128 printable ASCII characters")
	}
	return r, nil
}
