package auth_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/store"
)

// fakeIssuer is a minimal OpenID provider: discovery, JWKS and a token
// endpoint that checks PKCE and returns RS256-signed ID tokens.
type fakeIssuer struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]issued
	// tamper, when set, edits the ID token claims before signing.
	tamper func(map[string]any)
}

type issued struct {
	nonce, challenge string
	claims           map[string]any
}

const clientID = "beenfar-test"

func newFakeIssuer(t *testing.T) *fakeIssuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{t: t, key: key, codes: map[string]issued{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"end_session_endpoint":                  f.srv.URL + "/logout",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// authorize stands in for the user approving the login at the provider.
func (f *fakeIssuer) authorize(q url.Values, claims map[string]any) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := rand.Text()
	f.codes[code] = issued{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), claims: claims}
	return code
}

func (f *fakeIssuer) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	c, ok := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code"))
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || b64(sum[:]) != c.challenge {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	claims := map[string]any{
		"iss": f.srv.URL, "aud": clientID, "nonce": c.nonce,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
	maps.Copy(claims, c.claims)
	if f.tamper != nil {
		f.tamper(claims)
	}
	writeJSON(w, map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": f.sign(claims)})
}

func (f *fakeIssuer) sign(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	input := b64(header) + "." + b64(payload)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return input + "." + b64(sig)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type oidcHarness struct {
	t      *testing.T
	issuer *fakeIssuer
	oidc   *auth.OIDC
	svc    *auth.Service
	st     store.Store
}

func newOIDCHarness(t *testing.T) oidcHarness {
	issuer := newFakeIssuer(t)
	svc, st := newService(t)
	o, err := svc.OIDC([]auth.OIDCProvider{{
		ID: "corp", DisplayName: "Corp SSO", Issuer: issuer.srv.URL,
		ClientID: clientID, ClientSecret: "s3cret", AdminGroups: []string{"netadmins"},
	}}, "https://beenfar.test")
	if err != nil {
		t.Fatal(err)
	}
	return oidcHarness{t: t, issuer: issuer, oidc: o, svc: svc, st: st}
}

func (h oidcHarness) get(target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(h.t.Context(), http.MethodGet, target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.oidc.Handler().ServeHTTP(rec, req)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// start begins a login and returns the provider's authorize query and the
// state cookie.
func (h oidcHarness) start() (url.Values, *http.Cookie) {
	rec := h.get("/auth/oidc/corp/login?redirect=/devices")
	if rec.Code != http.StatusFound {
		h.t.Fatalf("login = %d %s", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(loc.String(), h.issuer.srv.URL+"/authorize") {
		h.t.Fatalf("redirect to %q", loc)
	}
	q := loc.Query()
	if q.Get("redirect_uri") != "https://beenfar.test/auth/oidc/corp/callback" || q.Get("code_challenge_method") != "S256" ||
		!strings.Contains(q.Get("scope"), "openid") || q.Get("nonce") == "" {
		h.t.Fatalf("authorize query = %v", q)
	}
	state := cookieNamed(rec, "beenfar_oidc_state")
	if state == nil || state.SameSite != http.SameSiteLaxMode || !state.HttpOnly {
		h.t.Fatalf("state cookie = %+v", state)
	}
	return q, state
}

func (h oidcHarness) callback(q url.Values, code string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return h.get("/auth/oidc/corp/callback?state="+url.QueryEscape(q.Get("state"))+"&code="+url.QueryEscape(code), cookies...)
}

func TestOIDCLogin(t *testing.T) {
	h := newOIDCHarness(t)
	ctx := t.Context()

	if got := h.oidc.Providers(); len(got) != 1 || got[0].LoginURL != "/auth/oidc/corp/login" || got[0].DisplayName != "Corp SSO" {
		t.Fatalf("providers = %+v", got)
	}

	q, state := h.start()
	code := h.issuer.authorize(q, map[string]any{"sub": "u-42", "preferred_username": "kim", "groups": []string{"staff", "netadmins"}})
	rec := h.callback(q, code, state)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/devices" {
		t.Fatalf("callback = %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	sess := cookieNamed(rec, auth.SessionCookieName)
	if sess == nil || sess.Value == "" || sess.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v", sess)
	}
	p, err := h.svc.AuthenticateSession(ctx, sess.Value)
	if err != nil || p.Method != auth.MethodOIDC || p.User.Username != "kim" || !p.IsAdmin() ||
		p.User.Source != store.UserSourceOIDC || p.SessionProvider != "oidc:corp" {
		t.Fatalf("principal = %+v, %v", p, err)
	}

	// The state works once.
	if rec := h.callback(q, code, state); rec.Code != http.StatusBadRequest {
		t.Errorf("replayed callback = %d", rec.Code)
	}

	// Signing in again maps to the same user even after an upstream rename,
	// and admin follows the groups.
	q, state = h.start()
	code = h.issuer.authorize(q, map[string]any{"sub": "u-42", "preferred_username": "kim.new", "groups": "staff"})
	rec = h.callback(q, code, state)
	if rec.Code != http.StatusFound {
		t.Fatalf("second login = %d %s", rec.Code, rec.Body)
	}
	p2, err := h.svc.AuthenticateSession(ctx, cookieNamed(rec, auth.SessionCookieName).Value)
	if err != nil || p2.User.ID != p.User.ID || p2.IsAdmin() {
		t.Fatalf("second principal = %+v, %v", p2, err)
	}

	if u := h.oidc.LogoutURL(ctx, "oidc:corp", httptest.NewRequest(http.MethodGet, "/", nil)); u !=
		h.issuer.srv.URL+"/logout?client_id=beenfar-test&post_logout_redirect_uri=https%3A%2F%2Fbeenfar.test%2F" {
		t.Errorf("logout url = %q", u)
	}
	if u := h.oidc.LogoutURL(ctx, "", httptest.NewRequest(http.MethodGet, "/", nil)); u != "" {
		t.Errorf("password session logout url = %q", u)
	}
}

func TestOIDCRejects(t *testing.T) {
	h := newOIDCHarness(t)
	claims := map[string]any{"sub": "u-1", "preferred_username": "lee"}

	// No state cookie: a callback link from someone else's login.
	q, _ := h.start()
	if rec := h.callback(q, h.issuer.authorize(q, claims)); rec.Code != http.StatusBadRequest {
		t.Errorf("missing state cookie = %d", rec.Code)
	}

	for name, tamper := range map[string]func(map[string]any){
		"nonce":    func(c map[string]any) { c["nonce"] = "other" },
		"audience": func(c map[string]any) { c["aud"] = "someone-else" },
		"issuer":   func(c map[string]any) { c["iss"] = "https://evil.example" },
		"expired":  func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
	} {
		h.issuer.tamper = tamper
		q, state := h.start()
		if rec := h.callback(q, h.issuer.authorize(q, claims), state); rec.Code != http.StatusUnauthorized || cookieNamed(rec, auth.SessionCookieName) != nil {
			t.Errorf("bad %s: %d", name, rec.Code)
		}
	}
	h.issuer.tamper = nil

	// A wrong PKCE verifier (here: a code for a different challenge) fails.
	q, state := h.start()
	q2 := url.Values{"nonce": {q.Get("nonce")}, "code_challenge": {"wrong"}}
	if rec := h.callback(q, h.issuer.authorize(q2, claims), state); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad PKCE = %d", rec.Code)
	}

	// Redirects stay on this site.
	rec := h.get("/auth/oidc/corp/login?redirect=//evil.example/x")
	state = cookieNamed(rec, "beenfar_oidc_state")
	loc, _ := url.Parse(rec.Header().Get("Location"))
	q = loc.Query()
	rec = h.callback(q, h.issuer.authorize(q, claims), state)
	if rec.Header().Get("Location") != "/" {
		t.Errorf("open redirect: %q", rec.Header().Get("Location"))
	}

	if rec := h.get("/auth/oidc/nope/login"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown provider = %d", rec.Code)
	}
	if _, err := h.svc.OIDC([]auth.OIDCProvider{{ID: "a/b", Issuer: "x", ClientID: "y"}}, ""); err == nil {
		t.Error("bad provider id accepted")
	}
}
