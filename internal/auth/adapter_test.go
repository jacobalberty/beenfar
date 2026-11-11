package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/store"
)

func TestAdapterTokens(t *testing.T) {
	svc, _ := newService(t)
	ctx := t.Context()

	for _, bad := range []string{"", "UniFi", "1abc", "a b", strings.Repeat("a", 33)} {
		if _, _, err := svc.CreateAdapterToken(ctx, bad); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("CreateAdapterToken(%q) = %v, want ErrInvalid", bad, err)
		}
	}

	tok, secret, err := svc.CreateAdapterToken(ctx, "meraki")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "bfa_") || strings.Contains(tok.SecretHash, secret) {
		t.Fatalf("secret %q, stored %+v", secret, tok)
	}
	name, err := svc.AuthenticateAdapterToken(ctx, secret)
	if err != nil || name != "meraki" {
		t.Fatalf("authenticate = %q, %v", name, err)
	}

	for _, bad := range []string{"", "bfa_", secret + "x", strings.Replace(secret, "bfa_", "bf_", 1), "bfa_aaaaaaaaaaaa_nope"} {
		if _, err := svc.AuthenticateAdapterToken(ctx, bad); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("AuthenticateAdapterToken(%q) = %v, want ErrUnauthenticated", bad, err)
		}
	}

	// An API token never authenticates as an adapter, and the other way round.
	if _, err := svc.AuthenticateToken(ctx, secret); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("adapter token accepted as API token: %v", err)
	}

	if err := svc.DeleteAdapterToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateAdapterToken(ctx, secret); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("revoked token accepted: %v", err)
	}
}
