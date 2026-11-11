package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/store"
)

func fixed(name string, err error) auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Request) (*auth.Principal, error) {
		if err != nil {
			return nil, err
		}
		return &auth.Principal{User: store.User{Username: name}}, nil
	})
}

func TestChain(t *testing.T) {
	ctx := t.Context()
	req := auth.Request{Header: http.Header{}}
	skip := fixed("", auth.ErrNoCredentials)

	p, err := auth.Chain{skip, fixed("second", nil), fixed("third", nil)}.Authenticate(ctx, req)
	if err != nil || p.User.Username != "second" {
		t.Fatalf("first match = %+v, %v", p, err)
	}

	// The first authenticator that sees its credentials decides, even if a
	// later one would accept the request.
	bad := fixed("", auth.ErrUnauthenticated)
	if _, err := (auth.Chain{bad, fixed("later", nil)}).Authenticate(ctx, req); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("invalid credentials fell through: %v", err)
	}

	if _, err := (auth.Chain{skip}).Authenticate(ctx, req); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("empty chain result: %v", err)
	}
	h := http.Header{"Authorization": {"Negotiate abc"}}
	_, err = auth.Chain{skip}.Authenticate(ctx, auth.Request{Header: h})
	if !errors.Is(err, auth.ErrUnauthenticated) || err.Error() != `unauthenticated: unsupported authorization scheme "Negotiate"` {
		t.Fatalf("unknown scheme: %v", err)
	}
}

func TestSessionCookieValue(t *testing.T) {
	h := http.Header{"Cookie": {"a=1; beenfar_session=xyz", "b=2"}}
	if got := auth.SessionCookieValue(h); got != "xyz" {
		t.Fatalf("cookie = %q", got)
	}
	if got := auth.SessionCookieValue(http.Header{}); got != "" {
		t.Fatalf("no cookie = %q", got)
	}
}
