package adapterhost_test

import (
	"errors"
	"testing"

	"github.com/jacobalberty/beenfar/api/adapter"
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

	if _, err := svc.Forget(ctx, d.ID, false); err != nil {
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
	if _, err := svc.Forget(ctx, locked.ID, false); err != nil {
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

// An adapter that will reset the device on its next contact keeps the device,
// marked as forgetting, until it reports the device released; then it is gone.
func TestForgetWaitsForTheAdapterToResetTheDevice(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	fa := newFake("fake")
	fa.forgetErr = adapter.ErrForgetDeferred
	svc := core.New(st, newRegistry(t, st, fa)).Devices
	host := <-fa.started

	d, err := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:30", Adapter: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Adopt(ctx, d.ID, ""); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Forget(ctx, d.ID, false)
	if err != nil || !got.Forgetting || got.State != store.DeviceStateAdopted {
		t.Fatalf("forget = %+v, %v", got, err)
	}
	if stored, _ := st.GetDevice(ctx, d.ID); !stored.Forgetting {
		t.Fatal("the device is not marked as forgetting in the store")
	}

	// Asking again changes nothing and does not bother the adapter again.
	if again, err := svc.Forget(ctx, d.ID, false); err != nil || !again.Forgetting || len(fa.forgotten) != 1 {
		t.Fatalf("second forget = %+v, %v, adapter told %d times", again, err, len(fa.forgotten))
	}
	// A device being reset is not offered for adoption in the meantime.
	if _, err := svc.Adopt(ctx, d.ID, ""); !errors.Is(err, store.ErrFailedPrecondition) {
		t.Fatalf("adopt while forgetting = %v", err)
	}
	// Adapters see that it is being forgotten.
	if dev, err := host.DeviceByMAC(ctx, d.MAC); err != nil || !dev.Forgetting {
		t.Fatalf("adapter's view = %+v, %v", dev, err)
	}

	// The adapter reports it reset: the controller drops it.
	if err := host.ReportState(ctx, d.ID, adapter.State{Released: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDevice(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("device still there after it was released: %v", err)
	}
}

// Released means nothing for a device the controller is not forgetting, so an
// adapter cannot drop a device that is still managed.
func TestReleasedIsIgnoredForADeviceThatIsNotBeingForgotten(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	fa := newFake("fake")
	newRegistry(t, st, fa)
	host := <-fa.started

	d, err := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:31", Adapter: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := host.ReportState(ctx, d.ID, adapter.State{Released: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDevice(ctx, d.ID); err != nil {
		t.Fatalf("a device that was not being forgotten was dropped: %v", err)
	}
}

// A forced forget gives up waiting: the adapter is told to drop what it holds
// (Forgetting set) and the device is removed at once.
func TestForceForgetGivesUpWaitingForTheReset(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	fa := newFake("fake")
	fa.forgetErr = adapter.ErrForgetDeferred
	svc := core.New(st, newRegistry(t, st, fa)).Devices

	d, err := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:32", Adapter: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Adopt(ctx, d.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Forget(ctx, d.ID, false); err != nil {
		t.Fatal(err)
	}

	fa.forgetErr = nil // the adapter drops its keys and says it is done
	gone, err := svc.Forget(ctx, d.ID, true)
	if err != nil || gone.ID != "" {
		t.Fatalf("forced forget = %+v, %v", gone, err)
	}
	last := fa.forgotten[len(fa.forgotten)-1]
	if !last.Forgetting {
		t.Fatal("the adapter was not told that the controller gave up waiting")
	}
	if _, err := st.GetDevice(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("device still there after a forced forget: %v", err)
	}
}

// A pending device has nothing to reset, whatever its adapter answers.
func TestForgetRemovesAPendingDeviceAtOnce(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	fa := newFake("fake")
	fa.forgetErr = adapter.ErrForgetDeferred
	svc := core.New(st, newRegistry(t, st, fa)).Devices

	d, err := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:33", Adapter: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Forget(ctx, d.ID, false); err != nil || got.ID != "" {
		t.Fatalf("forget = %+v, %v", got, err)
	}
	if _, err := st.GetDevice(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pending device still there: %v", err)
	}
}
