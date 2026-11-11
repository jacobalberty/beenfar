package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// SessionCookieName is the browser session cookie.
const SessionCookieName = "beenfar_session"

// ErrNoCredentials is returned by an Authenticator when the request carries
// no credentials of its kind, so the next one in a Chain should try.
var ErrNoCredentials = errors.New("no credentials")

// Header is the read side of a request's headers (http.Header and
// connect.Header both satisfy it).
type Header interface {
	Get(key string) string
	Values(key string) []string
}

// Request is what an Authenticator can see of an incoming request.
type Request struct {
	Header Header
	// Peer is the address of the TCP peer: the client, or the reverse proxy
	// in front of beenfar. Invalid when unknown.
	Peer netip.Addr
}

// Authenticator turns request credentials into a principal. It returns
// ErrNoCredentials when the request has none of its kind, an error wrapping
// ErrUnauthenticated when they are present but invalid, or another error.
type Authenticator interface {
	Authenticate(ctx context.Context, r Request) (*Principal, error)
}

// AuthenticatorFunc adapts a function to Authenticator.
type AuthenticatorFunc func(ctx context.Context, r Request) (*Principal, error)

func (f AuthenticatorFunc) Authenticate(ctx context.Context, r Request) (*Principal, error) {
	return f(ctx, r)
}

// Chain tries each authenticator in order. The first one that finds its
// kind of credentials decides: a request with an invalid bearer token is
// rejected even if it also has a valid cookie.
type Chain []Authenticator

func (c Chain) Authenticate(ctx context.Context, r Request) (*Principal, error) {
	for _, a := range c {
		p, err := a.Authenticate(ctx, r)
		if errors.Is(err, ErrNoCredentials) {
			continue
		}
		return p, err
	}
	if scheme, _, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok {
		return nil, fmt.Errorf("%w: unsupported authorization scheme %q", ErrUnauthenticated, scheme)
	}
	return nil, fmt.Errorf("%w: credentials required", ErrUnauthenticated)
}

// authorization returns the credentials of an Authorization header with the
// given scheme (case-insensitive).
func authorization(h Header, scheme string) (string, bool) {
	got, creds, ok := strings.Cut(h.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(got, scheme) {
		return "", false
	}
	return strings.TrimSpace(creds), true
}

// BearerToken authenticates "Authorization: Bearer <API token>".
func (s *Service) BearerToken() Authenticator {
	return AuthenticatorFunc(func(ctx context.Context, r Request) (*Principal, error) {
		token, ok := authorization(r.Header, "Bearer")
		if !ok {
			return nil, ErrNoCredentials
		}
		p, err := s.AuthenticateToken(ctx, token)
		if errors.Is(err, ErrUnauthenticated) {
			return nil, fmt.Errorf("%w: invalid or expired token", ErrUnauthenticated)
		}
		return p, err
	})
}

// SessionCookie authenticates the browser session cookie. Its principals are
// Ambient: browsers attach the cookie to requests on their own.
func (s *Service) SessionCookie() Authenticator {
	return AuthenticatorFunc(func(ctx context.Context, r Request) (*Principal, error) {
		cookie := SessionCookieValue(r.Header)
		if cookie == "" {
			return nil, ErrNoCredentials
		}
		p, err := s.AuthenticateSession(ctx, cookie)
		if errors.Is(err, ErrUnauthenticated) {
			return nil, fmt.Errorf("%w: session expired; sign in again", ErrUnauthenticated)
		}
		return p, err
	})
}

// SessionCookieValue returns the session cookie from Cookie headers.
func SessionCookieValue(h Header) string {
	for _, line := range h.Values("Cookie") {
		cookies, err := http.ParseCookie(line)
		if err != nil {
			continue
		}
		for _, c := range cookies {
			if c.Name == SessionCookieName {
				return c.Value
			}
		}
	}
	return ""
}
