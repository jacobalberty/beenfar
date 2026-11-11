package sqlite_test

import (
	"errors"
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

func TestExternalUsers(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	_, err := s.CreateExternalUser(ctx, store.User{Username: "x", Source: store.UserSourceLocal}, "proxy", "x")
	wantErr(t, err, store.ErrInvalid)
	_, err = s.CreateUser(ctx, store.User{Username: "x", PasswordHash: "h", Source: store.UserSourceOIDC})
	wantErr(t, err, store.ErrInvalid)

	u, err := s.CreateExternalUser(ctx, store.User{Username: "carol", Source: store.UserSourceOIDC}, "oidc:corp", "sub-123")
	if err != nil || u.Source != store.UserSourceOIDC || u.PasswordHash != "" {
		t.Fatalf("create = %+v, %v", u, err)
	}
	got, err := s.GetUserByIdentity(ctx, "oidc:corp", "sub-123")
	if err != nil || got.ID != u.ID {
		t.Fatalf("by identity = %+v, %v", got, err)
	}
	_, err = s.GetUserByIdentity(ctx, "oidc:other", "sub-123")
	wantErr(t, err, store.ErrNotFound)

	// The identity is unique, and so is the username; a failed create leaves
	// no half-made user behind.
	_, err = s.CreateExternalUser(ctx, store.User{Username: "carol2", Source: store.UserSourceOIDC}, "oidc:corp", "sub-123")
	wantErr(t, err, store.ErrConflict)
	if _, err := s.GetUserByUsername(ctx, "carol2"); err == nil {
		t.Error("user created despite identity conflict")
	}

	// Local users default to the local source; sessions keep their provider.
	l, err := s.CreateUser(ctx, store.User{Username: "dave", PasswordHash: "h"})
	if err != nil || l.Source != store.UserSourceLocal {
		t.Fatalf("local = %+v, %v", l, err)
	}
	sess, err := s.CreateSession(ctx, store.Session{IDHash: "s", UserID: u.ID, Provider: "oidc:corp", ExpireTime: time.Now().Add(time.Hour)})
	if err != nil || sess.Provider != "oidc:corp" {
		t.Fatalf("session = %+v, %v", sess, err)
	}

	// Deleting the user removes the identity link.
	if err := s.DeleteUser(ctx, u.ID, 0); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetUserByIdentity(ctx, "oidc:corp", "sub-123")
	wantErr(t, err, store.ErrNotFound)
}

func TestAdapterTokens(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	tok, err := s.CreateAdapterToken(ctx, store.AdapterToken{Adapter: "meraki", Prefix: "abc", SecretHash: "h"})
	if err != nil || tok.ID == "" || tok.Adapter != "meraki" || tok.CreateTime.IsZero() {
		t.Fatalf("create = %+v, %v", tok, err)
	}
	_, err = s.CreateAdapterToken(ctx, store.AdapterToken{Adapter: "meraki", Prefix: "abc", SecretHash: "h"})
	wantErr(t, err, store.ErrConflict)
	_, err = s.CreateAdapterToken(ctx, store.AdapterToken{Adapter: "", Prefix: "x", SecretHash: "h"})
	wantErr(t, err, store.ErrInvalid)

	now := time.Now().Truncate(time.Millisecond)
	if err := s.TouchAdapterToken(ctx, tok.ID, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAdapterTokenByPrefix(ctx, "abc")
	if err != nil || !got.LastUsedTime.Equal(now) {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if l, err := s.ListAdapterTokens(ctx); err != nil || len(l) != 1 {
		t.Fatalf("list = %v, %v", l, err)
	}
	if err := s.DeleteAdapterToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.DeleteAdapterToken(ctx, tok.ID), store.ErrNotFound)
	_, err = s.GetAdapterTokenByPrefix(ctx, "abc")
	wantErr(t, err, store.ErrNotFound)
}

func TestRadiusServers(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	_, err := s.CreateRadiusServer(ctx, store.RadiusServer{Name: "nosecret", Address: "10.0.0.2", AuthPort: 1812})
	wantErr(t, err, store.ErrInvalid)

	r, err := s.CreateRadiusServer(ctx, store.RadiusServer{
		Name: "corp", Address: "10.0.0.2", AuthPort: 1812, AccountingPort: 1813, SharedSecret: "s3cret",
	})
	if err != nil || r.ID == "" || r.Revision != 1 || r.SecretVersion != 1 || r.SharedSecret != "s3cret" {
		t.Fatalf("create = %+v, %v", r, err)
	}
	_, err = s.CreateRadiusServer(ctx, store.RadiusServer{Name: "corp", Address: "10.0.0.3", AuthPort: 1812, SharedSecret: "x"})
	wantErr(t, err, store.ErrConflict)

	// Changing other fields keeps the secret version; changing the secret
	// moves it.
	r.AuthPort = 11812
	r, err = s.UpdateRadiusServer(ctx, r)
	if err != nil || r.Revision != 2 || r.SecretVersion != 1 || r.AuthPort != 11812 {
		t.Fatalf("update = %+v, %v", r, err)
	}
	r.SharedSecret = "n3w"
	r, err = s.UpdateRadiusServer(ctx, r)
	if err != nil || r.Revision != 3 || r.SecretVersion != 2 {
		t.Fatalf("update secret = %+v, %v", r, err)
	}
	stale := r
	stale.Revision = 1
	_, err = s.UpdateRadiusServer(ctx, stale)
	wantErr(t, err, store.ErrStale)

	// Wireless networks may only refer to existing servers, and a server in
	// use cannot be deleted.
	enterprise := store.WifiNetwork{SSID: "corp", Security: store.WifiSecurityWPAEnterprise, Band: store.WifiBandBoth,
		RadiusServerID: "missing"}
	_, err = s.CreateWifiNetwork(ctx, enterprise)
	wantErr(t, err, store.ErrInvalid)
	enterprise.RadiusServerID = r.ID
	w, err := s.CreateWifiNetwork(ctx, enterprise)
	if err != nil || w.RadiusServerID != r.ID {
		t.Fatalf("wifi = %+v, %v", w, err)
	}
	wantErr(t, s.DeleteRadiusServer(ctx, r.ID, 0), store.ErrFailedPrecondition)

	w.RadiusServerID = ""
	if _, err := s.UpdateWifiNetwork(ctx, w); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.DeleteRadiusServer(ctx, r.ID, 1), store.ErrStale)
	if err := s.DeleteRadiusServer(ctx, r.ID, r.Revision); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.DeleteRadiusServer(ctx, r.ID, 0), store.ErrNotFound)
	if list, err := s.ListRadiusServers(ctx); err != nil || len(list) != 0 {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestSwitchPorts(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	d, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:10", Adapter: "unifi"})
	if err != nil {
		t.Fatal(err)
	}
	lan, err := s.CreateNetwork(ctx, store.Network{Name: "lan"})
	if err != nil {
		t.Fatal(err)
	}
	iot, err := s.CreateNetwork(ctx, store.Network{Name: "iot", VLANID: 20})
	if err != nil {
		t.Fatal(err)
	}
	cams, err := s.CreateNetwork(ctx, store.Network{Name: "cams", VLANID: 30})
	if err != nil {
		t.Fatal(err)
	}

	port := store.SwitchPort{
		DeviceID: d.ID, PortIndex: 3, Name: "uplink", NativeNetworkID: lan.ID,
		Tagging: store.SwitchPortTaggingCustom, TaggedNetworkIDs: []string{iot.ID, cams.ID, iot.ID},
		PoEMode: store.PoEModeOff,
	}
	p, err := s.CreateSwitchPort(ctx, port)
	if err != nil || p.ID == "" || p.Revision != 1 || len(p.TaggedNetworkIDs) != 2 || p.PoEMode != store.PoEModeOff {
		t.Fatalf("create = %+v, %v", p, err)
	}
	_, err = s.CreateSwitchPort(ctx, port)
	wantErr(t, err, store.ErrConflict) // same device and index

	for name, bad := range map[string]store.SwitchPort{
		"missing device": {DeviceID: "missing", PortIndex: 1, Tagging: store.SwitchPortTaggingAll, PoEMode: store.PoEModeAuto},
		"missing native": {DeviceID: d.ID, PortIndex: 1, NativeNetworkID: "missing", Tagging: store.SwitchPortTaggingAll, PoEMode: store.PoEModeAuto},
		"missing tagged": {DeviceID: d.ID, PortIndex: 1, Tagging: store.SwitchPortTaggingCustom, TaggedNetworkIDs: []string{"missing"}, PoEMode: store.PoEModeAuto},
		"index 0":        {DeviceID: d.ID, PortIndex: 0, Tagging: store.SwitchPortTaggingAll, PoEMode: store.PoEModeAuto},
	} {
		if _, err := s.CreateSwitchPort(ctx, bad); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}

	// Networks in use by a port cannot be deleted.
	wantErr(t, s.DeleteNetwork(ctx, lan.ID, 0), store.ErrFailedPrecondition)
	wantErr(t, s.DeleteNetwork(ctx, cams.ID, 0), store.ErrFailedPrecondition)

	// Update replaces the settings but never the device or index.
	p.TaggedNetworkIDs = []string{iot.ID}
	p.DeviceID, p.PortIndex = "other", 9
	upd, err := s.UpdateSwitchPort(ctx, p)
	if err != nil || upd.Revision != 2 || upd.DeviceID != d.ID || upd.PortIndex != 3 || len(upd.TaggedNetworkIDs) != 1 {
		t.Fatalf("update = %+v, %v", upd, err)
	}
	if err := s.DeleteNetwork(ctx, cams.ID, 0); err != nil {
		t.Fatalf("cams is no longer used: %v", err)
	}
	_, err = s.UpdateSwitchPort(ctx, p) // revision 1 is stale
	wantErr(t, err, store.ErrStale)

	if _, err := s.CreateSwitchPort(ctx, store.SwitchPort{DeviceID: d.ID, PortIndex: 1, Tagging: store.SwitchPortTaggingAll, PoEMode: store.PoEModeAuto}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSwitchPorts(ctx, d.ID)
	if err != nil || len(list) != 2 || list[0].PortIndex != 1 || list[1].PortIndex != 3 || list[1].TaggedNetworkIDs[0] != iot.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if other, err := s.ListSwitchPorts(ctx, "nobody"); err != nil || len(other) != 0 {
		t.Fatalf("list other = %+v, %v", other, err)
	}

	wantErr(t, s.DeleteSwitchPort(ctx, upd.ID, 1), store.ErrStale)
	if err := s.DeleteSwitchPort(ctx, upd.ID, upd.Revision); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.DeleteSwitchPort(ctx, upd.ID, 0), store.ErrNotFound)

	// Forgetting the device drops its port settings.
	if err := s.DeleteDevice(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if all, err := s.ListSwitchPorts(ctx, ""); err != nil || len(all) != 0 {
		t.Fatalf("after forget = %+v, %v", all, err)
	}
}
