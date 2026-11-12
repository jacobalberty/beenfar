package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
)

func open(t *testing.T) (*sqlite.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "beenfar.db")
	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	s, path := open(t)
	if _, err := s.ReportDevice(t.Context(), store.DiscoveredDevice{MAC: "de:ad:be:ef:00:01", Adapter: "unifi"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if _, err := s2.GetDeviceByMAC(t.Context(), "deadbeef0001"); err != nil {
		t.Fatalf("data lost across reopen: %v", err)
	}
}

func TestDevices(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	d, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "DE-AD-BE-EF-00-00", Adapter: "unifi", Model: "U6-Lite", IP: "10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if d.MAC != "de:ad:be:ef:00:00" || d.State != store.DeviceStatePending || d.ID == "" {
		t.Fatalf("unexpected device %+v", d)
	}

	// A second report keeps the ID and state, refreshes details, and keeps
	// known values when the report leaves them blank.
	d2, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "deadbeef0000", Adapter: "unifi", Firmware: "6.6.77"})
	if err != nil {
		t.Fatal(err)
	}
	if d2.ID != d.ID || d2.Model != "U6-Lite" || d2.IP != "10.0.0.5" || d2.Firmware != "6.6.77" {
		t.Fatalf("re-report changed identity or lost details: %+v", d2)
	}

	_, err = s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "not-a-mac", Adapter: "unifi"})
	wantErr(t, err, store.ErrInvalid)

	if _, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:02", Adapter: "unifi"}); err != nil {
		t.Fatal(err)
	}

	pending, err := s.ListDevices(ctx, store.DeviceFilter{State: store.DeviceStatePending})
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending = %d, %v; want 2", len(pending), err)
	}

	adopted, err := s.AdoptDevice(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.State != store.DeviceStateAdopted {
		t.Fatalf("state = %s", adopted.State)
	}
	_, err = s.AdoptDevice(ctx, d.ID)
	wantErr(t, err, store.ErrFailedPrecondition)
	_, err = s.AdoptDevice(ctx, "missing")
	wantErr(t, err, store.ErrNotFound)

	// Reporting an adopted device must not reset it to pending.
	again, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: d.MAC, Adapter: "unifi"})
	if err != nil || again.State != store.DeviceStateAdopted {
		t.Fatalf("re-report of adopted device: %+v, %v", again, err)
	}

	list, err := s.ListDevices(ctx, store.DeviceFilter{State: store.DeviceStateAdopted, Adapter: "unifi"})
	if err != nil || len(list) != 1 || list[0].ID != d.ID {
		t.Fatalf("adopted list = %+v, %v", list, err)
	}

	if err := s.DeleteDevice(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.DeleteDevice(ctx, d.ID), store.ErrNotFound)
	_, err = s.GetDevice(ctx, d.ID)
	wantErr(t, err, store.ErrNotFound)
}

func newWifi(ssid string) store.WifiNetwork {
	return store.WifiNetwork{
		SSID:        ssid,
		Security:    store.WifiSecurityWPAPersonal,
		SecurityKey: "correct horse",
		Band:        store.WifiBandBoth,
		APGroups:    []string{"upstairs", "downstairs", "upstairs"},
	}
}

func TestWifiNetworks(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	w, err := s.CreateWifiNetwork(ctx, newWifi("home"))
	if err != nil {
		t.Fatal(err)
	}
	if w.ID == "" || w.Revision != 1 || !slices.Equal(w.APGroups, []string{"downstairs", "upstairs"}) {
		t.Fatalf("unexpected create result %+v", w)
	}

	_, err = s.CreateWifiNetwork(ctx, newWifi("home"))
	wantErr(t, err, store.ErrConflict)

	bad := newWifi("bad")
	bad.Security = "rot13"
	_, err = s.CreateWifiNetwork(ctx, bad)
	wantErr(t, err, store.ErrInvalid)

	_, err = s.CreateWifiNetwork(ctx, newWifi(""))
	wantErr(t, err, store.ErrInvalid)

	got, err := s.GetWifiNetwork(ctx, w.ID)
	if err != nil || got.SSID != "home" || len(got.APGroups) != 2 {
		t.Fatalf("get = %+v, %v", got, err)
	}

	// Rename keeps the ID (the SSID is no longer the key).
	got.SSID = "home-renamed"
	got.APGroups = []string{"garage"}
	upd, err := s.UpdateWifiNetwork(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if upd.ID != w.ID || upd.Revision != 2 || !slices.Equal(upd.APGroups, []string{"garage"}) {
		t.Fatalf("unexpected update result %+v", upd)
	}

	// A stale revision is rejected; zero is unconditional.
	_, err = s.UpdateWifiNetwork(ctx, got) // still revision 1
	wantErr(t, err, store.ErrStale)
	upd.Revision = 0
	upd.Hidden = true
	upd, err = s.UpdateWifiNetwork(ctx, upd)
	if err != nil || upd.Revision != 3 || !upd.Hidden {
		t.Fatalf("unconditional update = %+v, %v", upd, err)
	}

	missing := newWifi("ghost")
	missing.ID = "missing"
	_, err = s.UpdateWifiNetwork(ctx, missing)
	wantErr(t, err, store.ErrNotFound)

	if _, err := s.CreateWifiNetwork(ctx, newWifi("guest")); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListWifiNetworks(ctx)
	if err != nil || len(list) != 2 || list[0].SSID != "guest" || !slices.Equal(list[1].APGroups, []string{"garage"}) {
		t.Fatalf("list = %+v, %v", list, err)
	}

	wantErr(t, s.DeleteWifiNetwork(ctx, w.ID, 1), store.ErrStale)
	if err := s.DeleteWifiNetwork(ctx, w.ID, 3); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.DeleteWifiNetwork(ctx, w.ID, 0), store.ErrNotFound)
}

func TestWithTxRollsBack(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()
	boom := errors.New("boom")

	err := s.WithTx(ctx, func(tx store.Store) error {
		if _, err := tx.CreateWifiNetwork(ctx, newWifi("rolled-back")); err != nil {
			return err
		}
		return boom
	})
	wantErr(t, err, boom)

	list, err := s.ListWifiNetworks(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("rollback left %d networks (%v)", len(list), err)
	}

	err = s.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateWifiNetwork(ctx, newWifi("committed"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListWifiNetworks(ctx); len(list) != 1 {
		t.Fatalf("commit: got %d networks", len(list))
	}
}

func TestConcurrentReports(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	errs := make(chan error, 20)
	for range 20 {
		go func() {
			_, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "aa:bb:cc:dd:ee:ff", Adapter: "unifi"})
			errs <- err
		}()
	}
	for range 20 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.ListDevices(ctx, store.DeviceFilter{})
	if len(list) != 1 {
		t.Fatalf("got %d devices, want 1", len(list))
	}
}

func TestDeviceLockedAndState(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	d, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:10", Adapter: "unifi", Locked: true})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Locked {
		t.Fatalf("device not locked: %+v", d)
	}
	// Reporting it again unlocked clears the flag.
	d, err = s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:10", Adapter: "unifi", Model: "U6-Lite"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Locked || d.Model != "U6-Lite" {
		t.Fatalf("unexpected device %+v", d)
	}

	got, err := s.RecordDeviceState(ctx, d.ID, store.DeviceReport{IP: "10.0.0.9", ConfigRevision: 42})
	if err != nil {
		t.Fatal(err)
	}
	if got.IP != "10.0.0.9" || got.ConfigRevision != 42 || got.Model != "U6-Lite" {
		t.Fatalf("unexpected state %+v", got)
	}
	_, err = s.RecordDeviceState(ctx, "nope", store.DeviceReport{})
	wantErr(t, err, store.ErrNotFound)
}

func TestDeviceSecrets(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	d, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:20", Adapter: "unifi"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetDeviceSecret(ctx, d.ID, "current")
	wantErr(t, err, store.ErrNotFound)

	if err := s.SetDeviceSecret(ctx, d.ID, "current", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceSecret(ctx, d.ID, "current", []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDeviceSecret(ctx, d.ID, "current")
	if err != nil || string(got) != "two" {
		t.Fatalf("got %q, %v; want two", got, err)
	}

	wantErr(t, s.SetDeviceSecret(ctx, "nope", "current", []byte("x")), store.ErrNotFound)
	wantErr(t, s.SetDeviceSecret(ctx, d.ID, "current", nil), store.ErrInvalid)

	if err := s.DeleteDeviceSecret(ctx, d.ID, "current"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDeviceSecret(ctx, d.ID, "current"); err != nil {
		t.Fatalf("deleting a missing secret: %v", err)
	}

	// Forgetting the device removes its secrets.
	if err := s.SetDeviceSecret(ctx, d.ID, "pending", []byte("p")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDevice(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetDeviceSecret(ctx, d.ID, "pending")
	wantErr(t, err, store.ErrNotFound)
}

func TestReadoption(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	d, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:40", Adapter: "unifi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdoptDevice(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	// An adopted device is not adopted again unless it is awaiting that.
	_, err = s.AdoptDevice(ctx, d.ID)
	wantErr(t, err, store.ErrFailedPrecondition)

	got, err := s.RecordDeviceState(ctx, d.ID, store.DeviceReport{Readoption: store.ReadoptionAwaiting})
	if err != nil || got.Readoption != store.ReadoptionAwaiting || got.State != store.DeviceStateAdopted {
		t.Fatalf("awaiting = %+v, %v", got, err)
	}
	// Accepting moves it on and leaves it adopted.
	got, err = s.AdoptDevice(ctx, d.ID)
	if err != nil || got.Readoption != store.ReadoptionInProgress || got.State != store.DeviceStateAdopted {
		t.Fatalf("accepted = %+v, %v", got, err)
	}
	// Accepting twice is refused: it is in progress, not awaiting.
	_, err = s.AdoptDevice(ctx, d.ID)
	wantErr(t, err, store.ErrFailedPrecondition)

	got, err = s.RecordDeviceState(ctx, d.ID, store.DeviceReport{})
	if err != nil || got.Readoption != store.ReadoptionNone {
		t.Fatalf("readoption did not clear: %+v, %v", got, err)
	}
}

func TestLockedReportsDoNotCountAsContact(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	d, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:41", Adapter: "unifi"})
	if err != nil {
		t.Fatal(err)
	}
	seen := d.LastSeen
	time.Sleep(5 * time.Millisecond)
	// A locked report is unauthenticated: it flags the device but does not
	// count as hearing from it.
	locked, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:41", Adapter: "unifi", Locked: true})
	if err != nil || !locked.Locked || !locked.LastSeen.Equal(seen) {
		t.Fatalf("locked report: %+v (last seen was %v), %v", locked, seen, err)
	}
	time.Sleep(5 * time.Millisecond)
	again, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:41", Adapter: "unifi"})
	if err != nil || !again.LastSeen.After(seen) || again.Locked {
		t.Fatalf("ordinary report: %+v, %v", again, err)
	}
}

func TestMarkDeviceForgetting(t *testing.T) {
	s, _ := open(t)
	ctx := t.Context()

	pending, err := s.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:40", Adapter: "unifi"})
	if err != nil {
		t.Fatal(err)
	}
	// There is nothing to reset on a device that is not adopted.
	_, err = s.MarkDeviceForgetting(ctx, pending.ID)
	wantErr(t, err, store.ErrFailedPrecondition)
	_, err = s.MarkDeviceForgetting(ctx, "missing")
	wantErr(t, err, store.ErrNotFound)

	if _, err := s.AdoptDevice(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.MarkDeviceForgetting(ctx, pending.ID)
	if err != nil || !got.Forgetting || got.State != store.DeviceStateAdopted {
		t.Fatalf("mark = %+v, %v", got, err)
	}
	// It stays marked across reads and reports, until the device is removed.
	if _, err := s.RecordDeviceState(ctx, pending.ID, store.DeviceReport{Model: "U7PG2"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetDevice(ctx, pending.ID); !got.Forgetting || got.Model != "U7PG2" {
		t.Fatalf("after a report: %+v", got)
	}
	if err := s.DeleteDevice(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
}
