package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/jacobalberty/beenfar/internal/store"
)

const (
	oidcProviderPrefix = "oidc:"
	oidcStateCookie    = "beenfar_oidc_state"
	oidcStateTTL       = 10 * time.Minute
	oidcPendingMax     = 10_000
)

// OIDCProvider configures one OpenID Connect identity provider.
type OIDCProvider struct {
	// ID names the provider in URLs (/auth/oidc/<ID>/login) and identities.
	ID           string
	DisplayName  string
	Issuer       string
	ClientID     string
	ClientSecret string
	// Scopes default to openid, profile, email (openid is always added).
	Scopes []string
	// UsernameClaim defaults to preferred_username; email and then sub are
	// used when the claim is missing.
	UsernameClaim string
	// GroupsClaim defaults to groups.
	GroupsClaim string
	// AdminGroups, when set, make admin status follow group membership at
	// every sign-in. When empty, admin status is managed in beenfar.
	AdminGroups []string
}

// OIDC runs the authorization-code flow (with PKCE) for configured providers
// and turns a successful sign-in into an ordinary session cookie.
type OIDC struct {
	svc       *Service
	publicURL string // optional; otherwise derived from each request
	providers map[string]*oidcProvider
	order     []string

	mu      sync.Mutex
	pending map[string]oidcPending // by state
}

type oidcProvider struct {
	cfg OIDCProvider

	mu        sync.Mutex
	discovery *oidc.Provider // nil until first use
}

type oidcPending struct {
	provider string
	nonce    string
	verifier string
	redirect string
	expires  time.Time
}

// OIDCProviderInfo is what a login screen needs to offer a provider.
type OIDCProviderInfo struct {
	ID, DisplayName, LoginURL string
}

// OIDC returns the OIDC sign-in handler for providers. publicURL is the
// externally visible base URL (e.g. https://beenfar.example.com); when empty
// it is derived from each request's Host and scheme.
func (s *Service) OIDC(providers []OIDCProvider, publicURL string) (*OIDC, error) {
	o := &OIDC{svc: s, publicURL: strings.TrimRight(publicURL, "/"), providers: map[string]*oidcProvider{}, pending: map[string]oidcPending{}}
	for _, p := range providers {
		switch {
		case p.ID == "" || strings.ContainsAny(p.ID, "/?#: "):
			return nil, fmt.Errorf("oidc: invalid provider id %q", p.ID)
		case o.providers[p.ID] != nil:
			return nil, fmt.Errorf("oidc: duplicate provider id %q", p.ID)
		case p.Issuer == "" || p.ClientID == "":
			return nil, fmt.Errorf("oidc provider %q: issuer and client id are required", p.ID)
		}
		if len(p.Scopes) == 0 {
			p.Scopes = []string{"profile", "email"}
		}
		if !slices.Contains(p.Scopes, oidc.ScopeOpenID) {
			p.Scopes = append([]string{oidc.ScopeOpenID}, p.Scopes...)
		}
		if p.UsernameClaim == "" {
			p.UsernameClaim = "preferred_username"
		}
		if p.GroupsClaim == "" {
			p.GroupsClaim = "groups"
		}
		if p.DisplayName == "" {
			p.DisplayName = p.ID
		}
		o.providers[p.ID] = &oidcProvider{cfg: p}
		o.order = append(o.order, p.ID)
	}
	return o, nil
}

// Providers lists the configured providers in configuration order.
func (o *OIDC) Providers() []OIDCProviderInfo {
	if o == nil {
		return nil
	}
	out := make([]OIDCProviderInfo, 0, len(o.order))
	for _, id := range o.order {
		out = append(out, OIDCProviderInfo{
			ID:          id,
			DisplayName: o.providers[id].cfg.DisplayName,
			LoginURL:    "/auth/oidc/" + url.PathEscape(id) + "/login",
		})
	}
	return out
}

// Handler serves GET /auth/oidc/{provider}/login and .../callback.
func (o *OIDC) Handler() http.Handler {
	known := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if o.providers[r.PathValue("provider")] == nil {
				http.NotFound(w, r)
				return
			}
			next(w, r)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/oidc/{provider}/login", known(o.login))
	mux.HandleFunc("GET /auth/oidc/{provider}/callback", known(o.callback))
	return mux
}

func (o *OIDC) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, cfg, err := o.oauth2Config(r)
	if err != nil {
		o.fail(w, r, http.StatusBadGateway, "identity provider unavailable", err)
		return
	}
	state, nonce, verifier := rand.Text(), rand.Text(), oauth2.GenerateVerifier()
	o.addPending(state, oidcPending{
		provider: p.cfg.ID,
		nonce:    nonce,
		verifier: verifier,
		redirect: safeRedirect(r.URL.Query().Get("redirect")),
		expires:  o.svc.now().Add(oidcStateTTL),
	})
	// Binds the flow to this browser, so a callback URL from someone else's
	// login cannot sign the victim in to the attacker's account. Lax, not
	// Strict: it must come back on the provider's top-level redirect.
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    state,
		Path:     "/auth/oidc/",
		MaxAge:   int(oidcStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	slog.DebugContext(ctx, "starting OIDC sign-in", slog.String("provider", p.cfg.ID))
	http.Redirect(w, r, cfg.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (o *OIDC) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	http.SetCookie(w, &http.Cookie{ // the state is single-use: clear it
		Name:     oidcStateCookie,
		Path:     "/auth/oidc/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})

	state := q.Get("state")
	c, err := r.Cookie(oidcStateCookie)
	if state == "" || err != nil || c.Value != state {
		o.fail(w, r, http.StatusBadRequest, "sign-in failed: state mismatch; start again", nil)
		return
	}
	pend, ok := o.takePending(state)
	if !ok || pend.provider != r.PathValue("provider") {
		o.fail(w, r, http.StatusBadRequest, "sign-in failed: expired or unknown state; start again", nil)
		return
	}
	if e := q.Get("error"); e != "" {
		o.fail(w, r, http.StatusUnauthorized, "sign-in refused by the identity provider", fmt.Errorf("%s: %s", e, q.Get("error_description")))
		return
	}

	p, cfg, err := o.oauth2Config(r)
	if err != nil {
		o.fail(w, r, http.StatusBadGateway, "identity provider unavailable", err)
		return
	}
	tok, err := cfg.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(pend.verifier))
	if err != nil {
		o.fail(w, r, http.StatusUnauthorized, "sign-in failed", fmt.Errorf("code exchange: %w", err))
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		o.fail(w, r, http.StatusUnauthorized, "sign-in failed", errors.New("token response has no id_token"))
		return
	}
	idt, err := p.discovery.Verifier(&oidc.Config{ClientID: p.cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		o.fail(w, r, http.StatusUnauthorized, "sign-in failed", fmt.Errorf("id token: %w", err))
		return
	}
	if idt.Nonce != pend.nonce {
		o.fail(w, r, http.StatusUnauthorized, "sign-in failed", errors.New("id token nonce mismatch"))
		return
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		o.fail(w, r, http.StatusUnauthorized, "sign-in failed", fmt.Errorf("id token claims: %w", err))
		return
	}

	username := firstString(claims, p.cfg.UsernameClaim, "email")
	if username == "" {
		username = idt.Subject
	}
	var admin *bool
	if len(p.cfg.AdminGroups) > 0 {
		groups := stringList(claims[p.cfg.GroupsClaim])
		a := slices.ContainsFunc(groups, func(g string) bool { return slices.Contains(p.cfg.AdminGroups, g) })
		admin = &a
	}
	provider := oidcProviderPrefix + p.cfg.ID
	u, err := o.svc.externalUser(ctx, store.UserSourceOIDC, provider, idt.Subject, username, admin)
	if err != nil {
		o.fail(w, r, http.StatusConflict, "sign-in failed: could not create your account", err)
		return
	}
	cookie, sess, err := o.svc.StartSession(ctx, u.ID, provider)
	if err != nil {
		o.fail(w, r, http.StatusInternalServerError, "sign-in failed", err)
		return
	}
	http.SetCookie(w, NewSessionCookie(cookie, sess.ExpireTime, requestIsSecure(r)))
	slog.InfoContext(ctx, "OIDC sign-in", slog.String("provider", p.cfg.ID), slog.String("username", u.Username))
	http.Redirect(w, r, pend.redirect, http.StatusFound)
}

// LogoutURL returns the provider's end-session URL for a session started by
// OIDC sign-in, or "" when there is none. requestBase is the scheme and host
// the browser used (e.g. "https://beenfar.example.com"); the configured
// public URL takes precedence.
func (o *OIDC) LogoutURL(ctx context.Context, sessionProvider, requestBase string) string {
	if o == nil {
		return ""
	}
	id, ok := strings.CutPrefix(sessionProvider, oidcProviderPrefix)
	if !ok || o.providers[id] == nil {
		return ""
	}
	p, err := o.discover(ctx, o.providers[id])
	if err != nil {
		slog.WarnContext(ctx, "OIDC discovery for logout", slog.Any("error", err))
		return ""
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	if err := p.discovery.Claims(&meta); err != nil || meta.EndSession == "" {
		return ""
	}
	u, err := url.Parse(meta.EndSession)
	if err != nil {
		return ""
	}
	v := u.Query()
	v.Set("client_id", p.cfg.ClientID)
	base := o.publicURL
	if base == "" {
		base = requestBase
	}
	v.Set("post_logout_redirect_uri", base+"/")
	u.RawQuery = v.Encode()
	return u.String()
}

func (o *OIDC) oauth2Config(r *http.Request) (*oidcProvider, *oauth2.Config, error) {
	p := o.providers[r.PathValue("provider")]
	if p == nil {
		return nil, nil, fmt.Errorf("unknown provider %q", r.PathValue("provider"))
	}
	if _, err := o.discover(r.Context(), p); err != nil {
		return nil, nil, err
	}
	return p, &oauth2.Config{
		ClientID:     p.cfg.ClientID,
		ClientSecret: p.cfg.ClientSecret,
		Endpoint:     p.discovery.Endpoint(),
		RedirectURL:  o.baseURL(r) + "/auth/oidc/" + url.PathEscape(p.cfg.ID) + "/callback",
		Scopes:       p.cfg.Scopes,
	}, nil
}

// discover fetches the provider's metadata once, retrying on later calls if
// it failed (the provider may be down when beenfard starts).
func (o *OIDC) discover(ctx context.Context, p *oidcProvider) (*oidcProvider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.discovery != nil {
		return p, nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	d, err := oidc.NewProvider(ctx, p.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc provider %q: discovery: %w", p.cfg.ID, err)
	}
	p.discovery = d
	return p, nil
}

func (o *OIDC) baseURL(r *http.Request) string {
	if o.publicURL != "" {
		return o.publicURL
	}
	scheme := "http"
	if requestIsSecure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (o *OIDC) addPending(state string, p oidcPending) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pending) >= oidcPendingMax {
		now := o.svc.now()
		for k, v := range o.pending {
			if !now.Before(v.expires) {
				delete(o.pending, k)
			}
		}
	}
	o.pending[state] = p
}

// takePending returns and forgets a pending flow, so each state works once.
func (o *OIDC) takePending(state string) (oidcPending, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	p, ok := o.pending[state]
	delete(o.pending, state)
	if !ok || !o.svc.now().Before(p.expires) {
		return oidcPending{}, false
	}
	return p, true
}

func (o *OIDC) fail(w http.ResponseWriter, r *http.Request, status int, msg string, err error) {
	if err != nil {
		slog.WarnContext(r.Context(), "OIDC sign-in failed",
			slog.String("provider", r.PathValue("provider")), slog.Any("error", err))
	}
	http.Error(w, msg, status)
}

// safeRedirect allows only local absolute paths, so the login flow cannot be
// used as an open redirect.
func safeRedirect(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	return p
}

func firstString(claims map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := claims[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// stringList reads a claim that is a list of strings or a single string.
func stringList(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// requestIsSecure reports whether the browser reached us over HTTPS,
// directly or through a proxy that says so.
func requestIsSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// NewSessionCookie builds the session cookie with beenfar's fixed
// attributes: HttpOnly, SameSite=Strict, Path=/, Secure over HTTPS.
func NewSessionCookie(value string, expires time.Time, secure bool) *http.Cookie {
	c := &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
	if value == "" {
		c.MaxAge = -1
	} else {
		c.Expires = expires
		c.MaxAge = max(int(time.Until(expires).Seconds()), 1)
	}
	return c
}
