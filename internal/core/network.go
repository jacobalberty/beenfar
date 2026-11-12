package core

import (
	"context"
	"net/netip"
	"strings"

	"github.com/jacobalberty/beenfar/internal/store"
)

// NetworkService manages VLANs and their subnets.
type NetworkService struct {
	store store.Store
}

func (s *NetworkService) Create(ctx context.Context, n store.Network) (store.Network, error) {
	n, err := normalizeNetwork(n)
	if err != nil {
		return store.Network{}, err
	}
	return networkTracking.create(ctx, s.store, func(tx store.Store) (store.Network, error) {
		return tx.CreateNetwork(ctx, n)
	})
}

func (s *NetworkService) Get(ctx context.Context, id string) (store.Network, error) {
	n, err := s.store.GetNetwork(ctx, id)
	if err != nil {
		return n, err
	}
	return n, networkTracking.load(ctx, s.store, &n)
}

func (s *NetworkService) List(ctx context.Context) ([]store.Network, error) {
	list, err := s.store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	return list, networkTracking.loadAll(ctx, s.store, list)
}

// networkFields are the mutable fields of a network, by API name.
var networkFields = fieldSetters[store.Network]{
	"name":         func(d *store.Network, s store.Network) { d.Name = s.Name },
	"vlan_id":      func(d *store.Network, s store.Network) { d.VLANID = s.VLANID },
	"subnet":       func(d *store.Network, s store.Network) { d.Subnet = s.Subnet },
	"dhcp_enabled": func(d *store.Network, s store.Network) { d.DHCPEnabled = s.DHCPEnabled },
	"dhcp_start":   func(d *store.Network, s store.Network) { d.DHCPStart = s.DHCPStart },
	"dhcp_stop":    func(d *store.Network, s store.Network) { d.DHCPStop = s.DHCPStop },
	"domain_name":  func(d *store.Network, s store.Network) { d.DomainName = s.DomainName },
}

// Update replaces the fields named by paths, or every mutable field when
// paths is empty or "*", like WifiService.Update.
func (s *NetworkService) Update(ctx context.Context, n store.Network, paths []string) (store.Network, error) {
	return updateMasked(ctx, s.store, n, paths, networkFields,
		func(n *store.Network) *int64 { return &n.Revision },
		func(ctx context.Context, tx store.Store) (store.Network, error) { return tx.GetNetwork(ctx, n.ID) },
		normalizeNetwork,
		func(ctx context.Context, tx store.Store, n store.Network) (store.Network, error) {
			return tx.UpdateNetwork(ctx, n)
		},
		networkTracking.afterWrite)
}

// Delete deletes by id. A non-zero revision must match. It fails while a
// wireless network refers to the network.
func (s *NetworkService) Delete(ctx context.Context, id string, revision int64) error {
	return s.store.DeleteNetwork(ctx, id, revision)
}

// normalizeNetwork validates n and puts its addresses in canonical form, so
// that what Terraform reads back matches what it wrote whenever the input was
// already canonical.
func normalizeNetwork(n store.Network) (store.Network, error) {
	if n.Name == "" || len(n.Name) > 64 {
		return n, invalid("name must be 1-64 bytes")
	}
	if n.VLANID < 0 || n.VLANID > 4094 {
		return n, invalid("vlan_id must be 0 (untagged) or 1-4094")
	}
	if len(n.DomainName) > 253 || strings.ContainsAny(n.DomainName, " \t\r\n/") {
		return n, invalid("domain_name is not a valid DNS name")
	}

	var prefix netip.Prefix
	if n.Subnet != "" {
		p, err := netip.ParsePrefix(n.Subnet)
		if err != nil || !p.Addr().Is4() {
			return n, invalid("subnet must be an IPv4 gateway address and prefix length, e.g. 192.168.10.1/24")
		}
		if p.Bits() > 30 {
			return n, invalid("subnet prefix length must be at most /30")
		}
		if p.Addr() == p.Masked().Addr() || p.Addr() == lastAddr(p) {
			return n, invalid("subnet must name the gateway address, not the network or broadcast address (e.g. 192.168.10.1/24)")
		}
		prefix = p
		n.Subnet = p.String()
	}

	var start, stop netip.Addr
	for _, f := range []struct {
		name string
		val  *string
		addr *netip.Addr
	}{{"dhcp_start", &n.DHCPStart, &start}, {"dhcp_stop", &n.DHCPStop, &stop}} {
		if *f.val == "" {
			continue
		}
		a, err := netip.ParseAddr(*f.val)
		if err != nil || !a.Is4() {
			return n, invalid("%s must be an IPv4 address", f.name)
		}
		if !prefix.IsValid() || !prefix.Contains(a) {
			return n, invalid("%s must be inside subnet", f.name)
		}
		*f.addr = a
		*f.val = a.String()
	}
	if n.DHCPEnabled {
		if !prefix.IsValid() {
			return n, invalid("dhcp_enabled requires subnet")
		}
		if !start.IsValid() || !stop.IsValid() {
			return n, invalid("dhcp_enabled requires dhcp_start and dhcp_stop")
		}
	}
	if start.IsValid() && stop.IsValid() && stop.Less(start) {
		return n, invalid("dhcp_start must not be after dhcp_stop")
	}
	return n, nil
}

// lastAddr returns the broadcast address of an IPv4 prefix.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As4()
	hostBits := 32 - p.Bits()
	for i := 3; i >= 0 && hostBits > 0; i-- {
		n := min(hostBits, 8)
		b[i] |= byte(1<<n - 1)
		hostBits -= n
	}
	return netip.AddrFrom4(b)
}
