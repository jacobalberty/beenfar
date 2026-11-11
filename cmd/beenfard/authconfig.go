package main

import (
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/server"
)

// authConfig holds the BEENFAR_AUTH_* settings. Each has a flag of the same
// name in lower-case kebab form; OIDC providers are environment-only because
// their variable names include the provider id.
type authConfig struct {
	publicURL string

	passwordLogin bool

	maxFailuresPerUser int
	maxFailuresPerIP   int
	failureWindow      time.Duration

	basic          bool
	basicPasswords bool
	basicCacheTTL  time.Duration

	proxyTrustedCIDRs string
	proxyUserHeader   string
	proxyGroupsHeader string
	proxyGroupsSep    string
	proxyAdminGroups  string
	proxyLogoutURL    string

	errs []error // malformed environment values
}

func (c *authConfig) envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s: %q is not a boolean", key, v))
	}
	return b
}

func (c *authConfig) envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s: %q is not an integer", key, v))
	}
	return n
}

func (c *authConfig) envDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s: %q is not a duration", key, v))
	}
	return d
}

// registerAuthFlags adds the auth flags to fs, defaulting from the environment.
func registerAuthFlags(fs *flag.FlagSet) *authConfig {
	c := &authConfig{}
	str := func(p *string, name, env, def, usage string) {
		fs.StringVar(p, name, envOr(env, def), usage+" (env "+env+")")
	}
	str(&c.publicURL, "public-url", "BEENFAR_PUBLIC_URL", "",
		"externally visible base URL, e.g. https://beenfar.example.com; used for OIDC redirects (default: from each request)")

	fs.BoolVar(&c.passwordLogin, "auth-password-login", c.envBool("BEENFAR_AUTH_PASSWORD_LOGIN", true),
		"offer the username/password login form (env BEENFAR_AUTH_PASSWORD_LOGIN)")
	fs.IntVar(&c.maxFailuresPerUser, "auth-max-failures-per-user", c.envInt("BEENFAR_AUTH_MAX_FAILURES_PER_USER", auth.DefaultMaxFailuresPerUser),
		"failed password sign-ins allowed per username per window; 0 disables (env BEENFAR_AUTH_MAX_FAILURES_PER_USER)")
	fs.IntVar(&c.maxFailuresPerIP, "auth-max-failures-per-ip", c.envInt("BEENFAR_AUTH_MAX_FAILURES_PER_IP", auth.DefaultMaxFailuresPerIP),
		"failed password sign-ins allowed per client address per window; 0 disables (env BEENFAR_AUTH_MAX_FAILURES_PER_IP)")
	fs.DurationVar(&c.failureWindow, "auth-failure-window", c.envDuration("BEENFAR_AUTH_FAILURE_WINDOW", auth.DefaultFailureWindow),
		"window for counting failed sign-ins (env BEENFAR_AUTH_FAILURE_WINDOW)")

	fs.BoolVar(&c.basic, "auth-basic", c.envBool("BEENFAR_AUTH_BASIC", false),
		"accept HTTP Basic auth with an API token as the password (env BEENFAR_AUTH_BASIC)")
	fs.BoolVar(&c.basicPasswords, "auth-basic-passwords", c.envBool("BEENFAR_AUTH_BASIC_PASSWORDS", false),
		"also accept users' real passwords over Basic auth; implies -auth-basic (env BEENFAR_AUTH_BASIC_PASSWORDS)")
	fs.DurationVar(&c.basicCacheTTL, "auth-basic-cache-ttl", c.envDuration("BEENFAR_AUTH_BASIC_CACHE_TTL", auth.DefaultBasicCacheTTL),
		"how long a verified Basic password is cached (env BEENFAR_AUTH_BASIC_CACHE_TTL)")

	str(&c.proxyTrustedCIDRs, "auth-proxy-trusted-cidrs", "BEENFAR_AUTH_PROXY_TRUSTED_CIDRS", "",
		"comma-separated peer CIDRs allowed to assert identity headers")
	str(&c.proxyUserHeader, "auth-proxy-user-header", "BEENFAR_AUTH_PROXY_USER_HEADER", "",
		"header holding the username set by a trusted proxy, e.g. X-Forwarded-User or Remote-User; enables proxy auth")
	str(&c.proxyGroupsHeader, "auth-proxy-groups-header", "BEENFAR_AUTH_PROXY_GROUPS_HEADER", "", "header holding the user's groups")
	str(&c.proxyGroupsSep, "auth-proxy-groups-separator", "BEENFAR_AUTH_PROXY_GROUPS_SEPARATOR", ",", "separator between groups in the groups header")
	str(&c.proxyAdminGroups, "auth-proxy-admin-groups", "BEENFAR_AUTH_PROXY_ADMIN_GROUPS", "",
		"comma-separated groups whose members are admins; when set, admin follows the groups header")
	str(&c.proxyLogoutURL, "auth-proxy-logout-url", "BEENFAR_AUTH_PROXY_LOGOUT_URL", "", "the proxy's sign-out URL, e.g. /oauth2/sign_out")
	return c
}

// serviceOptions are the auth.Service options from this configuration.
func (c *authConfig) serviceOptions() []auth.Option {
	return []auth.Option{auth.WithFailureLimits(c.maxFailuresPerUser, c.maxFailuresPerIP, c.failureWindow)}
}

// build returns the authenticator chain and the methods the API reports.
func (c *authConfig) build(svc *auth.Service, getenv func(string) string) (auth.Authenticator, server.AuthMethods, error) {
	if err := errors.Join(c.errs...); err != nil {
		return nil, server.AuthMethods{}, err
	}
	m := server.AuthMethods{
		DisablePasswordLogin: !c.passwordLogin,
		Basic:                c.basic || c.basicPasswords,
		BasicPasswords:       c.basicPasswords,
	}
	chain := auth.Chain{svc.BearerToken()}
	if m.Basic {
		chain = append(chain, svc.Basic(auth.BasicOptions{Passwords: c.basicPasswords, CacheTTL: c.basicCacheTTL}))
	}

	if c.proxyUserHeader != "" || c.proxyTrustedCIDRs != "" {
		var peers []netip.Prefix
		for _, s := range splitList(c.proxyTrustedCIDRs) {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, m, fmt.Errorf("BEENFAR_AUTH_PROXY_TRUSTED_CIDRS: %w", err)
			}
			peers = append(peers, p.Masked())
		}
		proxy, err := svc.Proxy(auth.ProxyOptions{
			TrustedPeers:    peers,
			UserHeader:      c.proxyUserHeader,
			GroupsHeader:    c.proxyGroupsHeader,
			GroupsSeparator: c.proxyGroupsSep,
			AdminGroups:     splitList(c.proxyAdminGroups),
		})
		if err != nil {
			return nil, m, fmt.Errorf("%w (set BEENFAR_AUTH_PROXY_USER_HEADER and BEENFAR_AUTH_PROXY_TRUSTED_CIDRS)", err)
		}
		chain = append(chain, proxy)
		m.Proxy, m.ProxyLogoutURL = true, c.proxyLogoutURL
	}

	providers, err := oidcProvidersFromEnv(getenv)
	if err != nil {
		return nil, m, err
	}
	if len(providers) > 0 {
		if m.OIDC, err = svc.OIDC(providers, c.publicURL); err != nil {
			return nil, m, err
		}
	}
	return append(chain, svc.SessionCookie()), m, nil
}

// oidcProvidersFromEnv reads BEENFAR_AUTH_OIDC_PROVIDERS (comma-separated
// ids) and, for each id, BEENFAR_AUTH_OIDC_<ID>_* where <ID> is the id in
// upper case with other characters replaced by "_".
func oidcProvidersFromEnv(getenv func(string) string) ([]auth.OIDCProvider, error) {
	var out []auth.OIDCProvider
	for _, id := range splitList(getenv("BEENFAR_AUTH_OIDC_PROVIDERS")) {
		prefix := "BEENFAR_AUTH_OIDC_" + envKey(id) + "_"
		get := func(k string) string { return strings.TrimSpace(getenv(prefix + k)) }
		secret := get("CLIENT_SECRET")
		if f := get("CLIENT_SECRET_FILE"); f != "" {
			b, err := os.ReadFile(f) //nolint:gosec // operator-supplied path
			if err != nil {
				return nil, fmt.Errorf("%sCLIENT_SECRET_FILE: %w", prefix, err)
			}
			secret = strings.TrimSpace(string(b))
		}
		p := auth.OIDCProvider{
			ID:            id,
			DisplayName:   get("DISPLAY_NAME"),
			Issuer:        get("ISSUER"),
			ClientID:      get("CLIENT_ID"),
			ClientSecret:  secret,
			Scopes:        strings.FieldsFunc(get("SCOPES"), func(r rune) bool { return r == ',' || r == ' ' }),
			UsernameClaim: get("USERNAME_CLAIM"),
			GroupsClaim:   get("GROUPS_CLAIM"),
			AdminGroups:   splitList(get("ADMIN_GROUPS")),
		}
		if p.Issuer == "" || p.ClientID == "" {
			return nil, fmt.Errorf("OIDC provider %q: set %sISSUER and %sCLIENT_ID", id, prefix, prefix)
		}
		out = append(out, p)
	}
	return out, nil
}

func envKey(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, id)
}

func splitList(s string) []string {
	var out []string
	for v := range strings.SplitSeq(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
