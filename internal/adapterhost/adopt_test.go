package adapterhost_test

import (
	"errors"
	"testing"

	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
)

func TestDeviceServiceCallsAdapter(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	fa := newFake("fake")
	reg := newRegistry(t, st, fa)
	svc := core.New(st, reg).Devices

	d, err := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:06", Adapter: "fake"})
	if err != nil {
		t.Fatal(err)
	}

	// A failing adapter leaves the device pending.
	fa.adoptErr = errors.New("boom")
	if _, err := svc.Adopt(ctx, d.ID, ""); err == nil {
		t.Fatal("adopt succeeded although the adapter failed")
	}
	if got, _ := st.GetDevice(ctx, d.ID); got.State != store.DeviceStatePending {
		t.Fatalf("state = %s", got.State)
	}

	fa.adoptErr = nil
	adopted, err := svc.Adopt(ctx, "", "de:ad:be:ef:00:06")
	if err != nil || adopted.State != store.DeviceStateAdopted {
		t.Fatalf("adopt = %+v, %v", adopted, err)
	}
	if _, err := svc.Adopt(ctx, d.ID, ""); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("adopting twice = %v", err)
	}

	if err := svc.Forget(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if len(fa.forgotten) != 1 || fa.forgotten[0].ID != d.ID {
		t.Fatalf("forgotten = %v", fa.forgotten)
	}
	if _, err := st.GetDevice(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("device still there: %v", err)
	}
}

func TestLockedDeviceCannotBeAdoptedAndOfflineAdapterCannotBlockForget(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	svc := core.New(st, newRegistry(t, st)).Devices // no adapters at all

	locked, err := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:07", Adapter: "gone", Locked: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Adopt(ctx, locked.ID, ""); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("adopting a locked device = %v", err)
	}
	if err := svc.Forget(ctx, locked.ID); err != nil {
		t.Fatalf("forget with the adapter offline: %v", err)
	}
	if _, err := st.GetDevice(ctx, locked.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("device still there: %v", err)
	}
}

// A reset device that is awaiting re-adoption is accepted with the same call
// that adopts a new one. It stays adopted and moves on to being re-keyed.
func TestAcceptingReadoption(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	fa := newFake("fake")
	svc := core.New(st, newRegistry(t, st, fa)).Devices

	d, err := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:08", Adapter: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Adopt(ctx, d.ID, ""); err != nil {
		t.Fatal(err)
	}
	// Adopted and healthy: nothing to accept.
	if _, err := svc.Adopt(ctx, d.ID, ""); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("adopting a healthy adopted device = %v", err)
	}

	if _, err := st.RecordDeviceState(ctx, d.ID, store.DeviceReport{Readoption: store.ReadoptionAwaiting}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Adopt(ctx, d.ID, "")
	if err != nil || got.State != store.DeviceStateAdopted || got.Readoption != store.ReadoptionInProgress {
		t.Fatalf("accept = %+v, %v", got, err)
	}
	if len(fa.adopted) != 2 {
		t.Fatalf("the adapter was told %d times, want 2 (adoption and acceptance)", len(fa.adopted))
	}
	// Accepted once: a second click does not re-key it again.
	if _, err := svc.Adopt(ctx, d.ID, ""); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("accepting twice = %v", err)
	}
}
