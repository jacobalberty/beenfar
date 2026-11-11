package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"

	"github.com/jacobalberty/beenfar/internal/store"
)

// proxyProvider is the identity provider name for proxy-asserted users.
const proxyProvider = "proxy"

// ProxyOptions configures trusted-proxy authentication. Nothing is specific
// to one proxy: the operator names the headers their proxy sets (e.g.
// X-Forwarded-User for oauth2-proxy, Remote-User for Authelia).
type ProxyOptions struct {
	// TrustedPeers are the addresses allowed to assert identity. Headers from
	// any other peer are ignored.
	TrustedPeers []netip.Prefix
	// UserHeader holds the username. Required.
	UserHeader string
	// GroupsHeader optionally holds the user's groups, separated by
	// GroupsSeparator (default ",").
	GroupsHeader    string
	GroupsSeparator string
	// AdminGroups, when set, make admin status follow group membership on
	// every request. When empty, admin status is managed in beenfar.
	AdminGroups []string
}

// Proxy authenticates users from identity headers set by a trusted reverse
// proxy. Users are created on first sight. The proxy's own session rides
// along on browser requests, so principals are Ambient.
func (s *Service) Proxy(opts ProxyOptions) (Authenticator, error) {
	if opts.UserHeader == "" {
		return nil, errors.New("proxy auth: a user header is required")
	}
	if len(opts.TrustedPeers) == 0 {
		return nil, errors.New("proxy auth: at least one trusted peer CIDR is required")
	}
	if opts.GroupsSeparator == "" {
		opts.GroupsSeparator = ","
	}
	return AuthenticatorFunc(func(ctx context.Context, r Request) (*Principal, error) {
		if !r.Peer.IsValid() || !slices.ContainsFunc(opts.TrustedPeers, func(p netip.Prefix) bool { return p.Contains(r.Peer) }) {
			return nil, ErrNoCredentials // never trust identity headers from elsewhere
		}
		subject := strings.TrimSpace(r.Header.Get(opts.UserHeader))
		if subject == "" {
			return nil, ErrNoCredentials
		}
		var admin *bool
		if len(opts.AdminGroups) > 0 {
			groups := splitGroups(r.Header.Get(opts.GroupsHeader), opts.GroupsSeparator, opts.GroupsHeader != "")
			a := slices.ContainsFunc(groups, func(g string) bool { return slices.Contains(opts.AdminGroups, g) })
			admin = &a
		}
		u, err := s.externalUser(ctx, store.UserSourceProxy, proxyProvider, subject, subject, admin)
		if err != nil {
			return nil, err
		}
		return &Principal{User: u, Method: MethodProxy, Ambient: true}, nil
	}), nil
}

func splitGroups(v, sep string, enabled bool) []string {
	if !enabled || v == "" {
		return nil
	}
	var out []string
	for g := range strings.SplitSeq(v, sep) {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// externalUser returns the user linked to (provider, subject), creating it
// on first sight with the preferred username (or "<username>@<provider>" if
// a different account already has that name). When admin is non-nil the
// user's admin flag is kept equal to it.
func (s *Service) externalUser(ctx context.Context, source store.UserSource, provider, subject, username string, admin *bool) (store.User, error) {
	u, err := s.store.GetUserByIdentity(ctx, provider, subject)
	if errors.Is(err, store.ErrNotFound) {
		u, err = s.createExternalUser(ctx, source, provider, subject, username, admin != nil && *admin)
		if err == nil {
			slog.InfoContext(ctx, "created external user",
				slog.String("username", u.Username), slog.String("provider", provider))
		}
		return u, err
	}
	if err != nil {
		return store.User{}, err
	}
	if admin != nil && u.Admin != *admin {
		u.Admin = *admin
		u.Revision = 0
		if u, err = s.store.UpdateUser(ctx, u); err != nil {
			return store.User{}, err
		}
		slog.InfoContext(ctx, "updated admin role from provider groups",
			slog.String("username", u.Username), slog.Bool("admin", u.Admin))
	}
	return u, nil
}

func (s *Service) createExternalUser(ctx context.Context, source store.UserSource, provider, subject, username string, admin bool) (store.User, error) {
	if len(username) > 64 {
		username = username[:64]
	}
	for _, name := range []string{username, username + "@" + provider} {
		u, err := s.store.CreateExternalUser(ctx, store.User{Username: name, Source: source, Admin: admin}, provider, subject)
		if !errors.Is(err, store.ErrConflict) {
			return u, err
		}
		// A concurrent request may have linked the identity first.
		if u, err := s.store.GetUserByIdentity(ctx, provider, subject); err == nil {
			return u, nil
		}
	}
	return store.User{}, fmt.Errorf("%w: username %q is taken by another account", store.ErrConflict, username)
}
