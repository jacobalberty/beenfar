package auth_test

import (
	"errors"
	"net/http"
	"net/netip"
	"testing"

	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/store"
)

func proxyReq(peer, user, groups string) auth.Request {
	h := http.Header{}
	if user != "" {
		h.Set("Remote-User", user)
	}
	if groups != "" {
		h.Set("Remote-Groups", groups)
	}
	return auth.Request{Header: h, Peer: netip.MustParseAddr(peer)}
}

func TestProxy(t *testing.T) {
	svc, st := newService(t)
	ctx := t.Context()
	if _, err := svc.Proxy(auth.ProxyOptions{UserHeader: "Remote-User"}); err == nil {
		t.Fatal("proxy auth without trusted peers accepted")
	}
	a, err := svc.Proxy(auth.ProxyOptions{
		TrustedPeers: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		UserHeader:   "Remote-User",
		GroupsHeader: "Remote-Groups",
		AdminGroups:  []string{"netadmins"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Headers from an untrusted peer are ignored, not an error.
	if _, err := a.Authenticate(ctx, proxyReq("192.0.2.1", "mallory", "netadmins")); !errors.Is(err, auth.ErrNoCredentials) {
		t.Fatalf("untrusted peer: %v", err)
	}
	if _, err := a.Authenticate(ctx, proxyReq("10.1.2.3", "", "")); !errors.Is(err, auth.ErrNoCredentials) {
		t.Fatalf("no user header: %v", err)
	}

	p, err := a.Authenticate(ctx, proxyReq("10.1.2.3", "henry", "staff, netadmins"))
	if err != nil || p.Method != auth.MethodProxy || !p.Ambient || !p.IsAdmin() || p.User.Source != store.UserSourceProxy {
		t.Fatalf("first request = %+v, %v", p, err)
	}
	id := p.User.ID

	// Leaving the admin group demotes on the next request; same account.
	p, err = a.Authenticate(ctx, proxyReq("10.1.2.3", "henry", "staff"))
	if err != nil || p.User.ID != id || p.IsAdmin() {
		t.Fatalf("after group change = %+v, %v", p, err)
	}

	// A local account with the same name is not taken over.
	local, err := svc.CreateUser(ctx, "ivy", "password1", true)
	if err != nil {
		t.Fatal(err)
	}
	p, err = a.Authenticate(ctx, proxyReq("10.1.2.3", "ivy", ""))
	if err != nil || p.User.ID == local.ID || p.User.Username != "ivy@proxy" || p.IsAdmin() {
		t.Fatalf("name clash = %+v, %v", p, err)
	}
	if u, _ := st.GetUser(ctx, local.ID); !u.Admin {
		t.Error("local user was modified")
	}
}
