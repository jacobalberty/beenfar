package auth_test

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/jacobalberty/beenfar/internal/auth"
)

func basicReq(user, pass string) auth.Request {
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	return auth.Request{Header: h, Peer: netip.MustParseAddr("192.0.2.7")}
}

func TestBasic(t *testing.T) {
	now := time.Now()
	svc, st := newService(t, auth.WithClock(func() time.Time { return now }))
	ctx := t.Context()
	u, err := svc.CreateUser(ctx, "gina", "password1", false)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := svc.CreateAPIToken(ctx, u.ID, "git", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	tokensOnly := svc.Basic(auth.BasicOptions{})
	if _, err := tokensOnly.Authenticate(ctx, auth.Request{Header: http.Header{}}); !errors.Is(err, auth.ErrNoCredentials) {
		t.Fatalf("no header: %v", err)
	}
	p, err := tokensOnly.Authenticate(ctx, basicReq("anything", token))
	if err != nil || p.User.ID != u.ID || p.Method != auth.MethodToken || !p.Ambient {
		t.Fatalf("token as password = %+v, %v", p, err)
	}
	if _, err := tokensOnly.Authenticate(ctx, basicReq("gina", "password1")); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("password with tokens-only: %v", err)
	}
	h := http.Header{"Authorization": {"Basic !!!"}}
	if _, err := tokensOnly.Authenticate(ctx, auth.Request{Header: h}); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("malformed: %v", err)
	}

	withPasswords := svc.Basic(auth.BasicOptions{Passwords: true, CacheTTL: time.Minute})
	p, err = withPasswords.Authenticate(ctx, basicReq("gina", "password1"))
	if err != nil || p.Method != auth.MethodBasic || !p.Ambient {
		t.Fatalf("password = %+v, %v", p, err)
	}
	if _, err := withPasswords.Authenticate(ctx, basicReq("gina", "wrong-pass")); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("wrong password: %v", err)
	}

	// A cached password stops working as soon as it is changed.
	if err := svc.SetPassword(ctx, u.ID, "password2", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := withPasswords.Authenticate(ctx, basicReq("gina", "password1")); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("old password after change: %v", err)
	}
	if _, err := withPasswords.Authenticate(ctx, basicReq("gina", "password2")); err != nil {
		t.Fatalf("new password: %v", err)
	}

	// Cache hits do not hide a deleted user.
	if err := st.DeleteUser(ctx, u.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := withPasswords.Authenticate(ctx, basicReq("gina", "password2")); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("deleted user: %v", err)
	}
}
