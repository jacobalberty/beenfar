package auth_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
)

func newService(t *testing.T, opts ...auth.Option) (*auth.Service, store.Store) {
	t.Helper()
	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "beenfar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return auth.New(st, append([]auth.Option{auth.WithIterations(1000)}, opts...)...), st
}

func TestPasswordHash(t *testing.T) {
	h, err := auth.HashPassword("correct horse", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$pbkdf2-sha256$i=1000$") {
		t.Fatalf("hash = %q", h)
	}
	h2, _ := auth.HashPassword("correct horse", 1000)
	if h == h2 {
		t.Error("hashes are not salted")
	}
	for pw, want := range map[string]bool{"correct horse": true, "correct hors": false, "": false} {
		if ok, err := auth.CheckPassword(h, pw); err != nil || ok != want {
			t.Errorf("CheckPassword(%q) = %v, %v; want %v", pw, ok, err, want)
		}
	}
	for _, bad := range []string{"", "plain", "$pbkdf2-sha256$i=x$a$b", "$bcrypt$i=1$a$b", "$pbkdf2-sha256$i=1$!!$b"} {
		if _, err := auth.CheckPassword(bad, "x"); err == nil {
			t.Errorf("CheckPassword accepted malformed hash %q", bad)
		}
	}
}

func TestTokens(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	svc, st := newService(t, auth.WithClock(clock))
	ctx := t.Context()
	u, err := svc.CreateUser(ctx, "alice", "password1", false)
	if err != nil {
		t.Fatal(err)
	}

	tok, secret, err := svc.CreateAPIToken(ctx, u.ID, "ci", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "bf_"+tok.Prefix+"_") || strings.Contains(tok.SecretHash, secret) {
		t.Fatalf("secret %q, token %+v", secret, tok)
	}
	p, err := svc.AuthenticateToken(ctx, secret)
	if err != nil || p.User.ID != u.ID || p.Method != auth.MethodToken || p.IsAdmin() {
		t.Fatalf("principal = %+v, %v", p, err)
	}
	if got, _ := st.GetAPIToken(ctx, tok.ID); got.LastUsedTime.IsZero() {
		t.Error("last-used time not recorded")
	}

	for _, bad := range []string{"", "bf_", secret + "x", "bf_" + tok.Prefix + "_wrong", "xx" + secret[2:]} {
		if _, err := svc.AuthenticateToken(ctx, bad); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("token %q: %v, want ErrUnauthenticated", bad, err)
		}
	}

	now = now.Add(2 * time.Hour) // past expiry
	if _, err := svc.AuthenticateToken(ctx, secret); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("expired token: %v", err)
	}
	if _, _, err := svc.CreateAPIToken(ctx, u.ID, "past", now.Add(-time.Second)); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("token expiring in the past: %v", err)
	}
}

func TestSessions(t *testing.T) {
	now := time.Now()
	svc, _ := newService(t, auth.WithClock(func() time.Time { return now }), auth.WithSessionTTL(time.Hour))
	ctx := t.Context()
	u, err := svc.CreateUser(ctx, "bob", "password1", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Login(ctx, "bob", "wrong-password"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("bad password: %v", err)
	}
	if _, _, _, err := svc.Login(ctx, "nobody", "password1"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("unknown user: %v", err)
	}
	_, cookie, sess, err := svc.Login(ctx, "bob", "password1")
	if err != nil || !sess.ExpireTime.Equal(now.Add(time.Hour).Truncate(time.Millisecond).UTC()) {
		t.Fatalf("login = %+v, %v", sess, err)
	}
	p, err := svc.AuthenticateSession(ctx, cookie)
	if err != nil || p.User.ID != u.ID || !p.IsAdmin() {
		t.Fatalf("session principal = %+v, %v", p, err)
	}

	// A password change ends every session.
	if err := svc.SetPassword(ctx, u.ID, "password2", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateSession(ctx, cookie); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("session survived password change: %v", err)
	}

	_, cookie, _, err = svc.Login(ctx, "bob", "password2")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := svc.AuthenticateSession(ctx, cookie); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("expired session: %v", err)
	}
}

func TestBootstrapAndLastAdmin(t *testing.T) {
	svc, st := newService(t)
	ctx := t.Context()

	if _, err := svc.Bootstrap(ctx, "admin", "short"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("short bootstrap password: %v", err)
	}
	created, err := svc.Bootstrap(ctx, "admin", "password1")
	if err != nil || !created {
		t.Fatalf("bootstrap = %v, %v", created, err)
	}
	created, err = svc.Bootstrap(ctx, "other", "password1")
	if err != nil || created {
		t.Fatalf("second bootstrap = %v, %v", created, err)
	}

	admin, _ := st.GetUserByUsername(ctx, "admin")
	admin.Admin = false
	if _, err := svc.UpdateUser(ctx, admin); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("demoting last admin: %v", err)
	}
	if err := svc.DeleteUser(ctx, admin.ID, 0); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("deleting last admin: %v", err)
	}
	if _, err := svc.CreateUser(ctx, "second", "password1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateUser(ctx, admin); err != nil {
		t.Fatalf("demoting one of two admins: %v", err)
	}

	wrong := "nope"
	if err := svc.SetPassword(ctx, admin.ID, "password9", &wrong); !errors.Is(err, auth.ErrPermissionDenied) {
		t.Fatalf("wrong current password: %v", err)
	}
}
