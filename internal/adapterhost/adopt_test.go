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
