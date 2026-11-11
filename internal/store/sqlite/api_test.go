package sqlite_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jacobalberty/beenfar/internal/store"
)

func TestNetworks(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	n, err := s.CreateNetwork(ctx, store.Network{Name: "iot", VLANID: 20, Subnet: "10.0.20.1/24"})
	if err != nil || n.ID == "" || n.Revision != 1 {
		t.Fatalf("create = %+v, %v", n, err)
	}
	// Tagged VLAN ids are unique; untagged (0) ones are not.
	_, err = s.CreateNetwork(ctx, store.Network{Name: "iot-2", VLANID: 20})
	wantErr(t, err, store.ErrConflict)
	if !strings.Contains(err.Error(), "vlan_id already in use") {
		t.Errorf("unfriendly conflict message: %v", err)
	}
	for _, name := range []string{"lan-a", "lan-b"} {
		if _, err := s.CreateNetwork(ctx, store.Network{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.CreateNetwork(ctx, store.Network{Name: "bad", VLANID: 5000})
	wantErr(t, err, store.ErrInvalid)

	// Wireless networks may only refer to existing networks...
	_, err = s.CreateWifiNetwork(ctx, store.WifiNetwork{SSID: "x", Security: store.WifiSecurityOpen, Band: store.WifiBandBoth, NetworkID: "missing"})
	wantErr(t, err, store.ErrInvalid)
	w, err := s.CreateWifiNetwork(ctx, store.WifiNetwork{SSID: "x", Security: store.WifiSecurityOpen, Band: store.WifiBandBoth, NetworkID: n.ID})
	if err != nil {
		t.Fatal(err)
	}
	// ...and a referenced network cannot be deleted.
	wantErr(t, s.DeleteNetwork(ctx, n.ID, 0), store.ErrFailedPrecondition)

	n.VLANID = 30
	upd, err := s.UpdateNetwork(ctx, n)
	if err != nil || upd.Revision != 2 || upd.VLANID != 30 {
		t.Fatalf("update = %+v, %v", upd, err)
	}
	_, err = s.UpdateNetwork(ctx, n) // revision 1 is stale
	wantErr(t, err, store.ErrStale)

	if err := s.DeleteWifiNetwork(ctx, w.ID, 0); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.DeleteNetwork(ctx, n.ID, 1), store.ErrStale)
	if err := s.DeleteNetwork(ctx, n.ID, 2); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.DeleteNetwork(ctx, n.ID, 0), store.ErrNotFound)

	list, err := s.ListNetworks(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "lan-a" {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestUsersTokensSessions(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	u, err := s.CreateUser(ctx, store.User{Username: "alice", PasswordHash: "h1", Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateUser(ctx, store.User{Username: "alice", PasswordHash: "h2"})
	wantErr(t, err, store.ErrConflict)
	if n, err := s.CountAdmins(ctx); err != nil || n != 1 {
		t.Fatalf("admins = %d, %v", n, err)
	}

	u.Username = "alice2"
	u, err = s.UpdateUser(ctx, u)
	if err != nil || u.Revision != 2 || u.PasswordHash != "h1" {
		t.Fatalf("update = %+v, %v", u, err)
	}
	if err := s.SetUserPassword(ctx, u.ID, "h3"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetUserByUsername(ctx, "alice2"); got.PasswordHash != "h3" || got.Revision != 2 {
		t.Fatalf("after password change = %+v", got)
	}

	expire := time.Now().Add(time.Hour).Truncate(time.Millisecond).UTC()
	tok, err := s.CreateAPIToken(ctx, store.APIToken{UserID: u.ID, Name: "tf", Prefix: "abc", SecretHash: "x", ExpireTime: expire})
	if err != nil || !tok.ExpireTime.Equal(expire) || !tok.LastUsedTime.IsZero() {
		t.Fatalf("token = %+v, %v", tok, err)
	}
	_, err = s.CreateAPIToken(ctx, store.APIToken{UserID: "nobody", Name: "tf", Prefix: "def", SecretHash: "x"})
	wantErr(t, err, store.ErrInvalid)
	now := time.Now()
	if err := s.TouchAPIToken(ctx, tok.ID, now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetAPITokenByPrefix(ctx, "abc"); err != nil || got.LastUsedTime.UnixMilli() != now.UnixMilli() {
		t.Fatalf("by prefix = %+v, %v", got, err)
	}

	if _, err := s.CreateSession(ctx, store.Session{IDHash: "old", UserID: u.ID, ExpireTime: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, store.Session{IDHash: "new", UserID: u.ID, ExpireTime: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteExpiredSessions(ctx, now); err != nil || n != 1 {
		t.Fatalf("expired = %d, %v", n, err)
	}

	// Deleting the user cascades to tokens and sessions.
	if err := s.DeleteUser(ctx, u.ID, 0); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetAPIToken(ctx, tok.ID)
	wantErr(t, err, store.ErrNotFound)
	_, err = s.GetSession(ctx, "new")
	wantErr(t, err, store.ErrNotFound)
}
