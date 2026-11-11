package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
)

func newAuthService(t *testing.T) *auth.Service {
	t.Helper()
	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return auth.New(st)
}

func TestAuthConfigDefaults(t *testing.T) {
	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	authn, m, err := cfg.auth.build(newAuthService(t), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if m.DisablePasswordLogin || m.Basic || m.Proxy || m.OIDC != nil {
		t.Errorf("defaults enable external auth: %+v", m)
	}
	if chain := authn.(auth.Chain); len(chain) != 2 { // bearer token, session cookie
		t.Errorf("default chain has %d authenticators", len(chain))
	}
}

func TestAuthConfigFromEnv(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"BEENFAR_AUTH_PASSWORD_LOGIN":      "false",
		"BEENFAR_AUTH_BASIC_PASSWORDS":     "true",
		"BEENFAR_AUTH_PROXY_TRUSTED_CIDRS": "10.0.0.0/8, fd00::/8",
		"BEENFAR_AUTH_PROXY_USER_HEADER":   "Remote-User",
		"BEENFAR_AUTH_PROXY_LOGOUT_URL":    "/logout",
	} {
		t.Setenv(k, v)
	}
	env := map[string]string{
		"BEENFAR_AUTH_OIDC_PROVIDERS":               "corp, home-lab",
		"BEENFAR_AUTH_OIDC_CORP_ISSUER":             "https://sso.example.com",
		"BEENFAR_AUTH_OIDC_CORP_CLIENT_ID":          "beenfar",
		"BEENFAR_AUTH_OIDC_CORP_CLIENT_SECRET_FILE": secret,
		"BEENFAR_AUTH_OIDC_CORP_ADMIN_GROUPS":       "netadmins",
		"BEENFAR_AUTH_OIDC_HOME_LAB_ISSUER":         "https://id.home.lab",
		"BEENFAR_AUTH_OIDC_HOME_LAB_CLIENT_ID":      "bf",
		"BEENFAR_AUTH_OIDC_HOME_LAB_DISPLAY_NAME":   "Home",
		"BEENFAR_AUTH_OIDC_HOME_LAB_SCOPES":         "openid email groups",
	}
	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	authn, m, err := cfg.auth.build(newAuthService(t), func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if !m.DisablePasswordLogin || !m.Basic || !m.BasicPasswords || !m.Proxy || m.ProxyLogoutURL != "/logout" {
		t.Errorf("methods = %+v", m)
	}
	if ps := m.OIDC.Providers(); len(ps) != 2 || ps[1].ID != "home-lab" || ps[1].DisplayName != "Home" {
		t.Errorf("providers = %+v", ps)
	}
	if chain := authn.(auth.Chain); len(chain) != 4 { // token, basic, proxy, session
		t.Errorf("chain has %d authenticators", len(chain))
	}

	providers, err := oidcProvidersFromEnv(func(k string) string { return env[k] })
	if err != nil || providers[0].ClientSecret != "s3cret" || providers[0].AdminGroups[0] != "netadmins" {
		t.Errorf("corp provider = %+v, %v", providers, err)
	}
}

func TestAuthConfigErrors(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"bad bool":       {"BEENFAR_AUTH_BASIC": "maybe"},
		"bad cidr":       {"BEENFAR_AUTH_PROXY_TRUSTED_CIDRS": "10.0.0.0/33", "BEENFAR_AUTH_PROXY_USER_HEADER": "U"},
		"header no cidr": {"BEENFAR_AUTH_PROXY_USER_HEADER": "Remote-User"},
		"cidr no header": {"BEENFAR_AUTH_PROXY_TRUSTED_CIDRS": "10.0.0.0/8"},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			cfg, err := parseFlags(nil)
			if err != nil {
				return // rejected at parse time is fine too
			}
			if _, _, err := cfg.auth.build(newAuthService(t), func(string) string { return "" }); err == nil {
				t.Error("accepted")
			}
		})
	}
	cfg, _ := parseFlags(nil)
	_, _, err := cfg.auth.build(newAuthService(t), func(k string) string {
		return map[string]string{"BEENFAR_AUTH_OIDC_PROVIDERS": "corp"}[k]
	})
	if err == nil || !strings.Contains(err.Error(), "BEENFAR_AUTH_OIDC_CORP_ISSUER") {
		t.Errorf("incomplete OIDC provider: %v", err)
	}
}
