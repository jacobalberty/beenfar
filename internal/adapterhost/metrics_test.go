package adapterhost_test

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
)

func TestMetrics(t *testing.T) {
	ctx := t.Context()
	st := openStore(t)
	reader := sdkmetric.NewManualReader()
	reg, err := adapterhost.NewRegistry(adapterhost.Options{Store: st, Meter: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")})
	if err != nil {
		t.Fatal(err)
	}
	fa := newFake("fake")
	if err := reg.AddBuiltin(fa); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	reg.Start(runCtx)
	t.Cleanup(func() { cancel(); reg.Wait() })
	svc := core.New(st, reg).Devices

	a, _ := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:31", Adapter: "fake"})
	for _, d := range []store.DiscoveredDevice{
		{MAC: "de:ad:be:ef:00:32", Adapter: "fake"},
		{MAC: "de:ad:be:ef:00:33", Adapter: "fake", Locked: true},
	} {
		if _, err := st.ReportDevice(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Adopt(ctx, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Adopt(ctx, a.ID, ""); err == nil { // already adopted: rejected before the adapter
		t.Fatal("adopted twice")
	}
	other, _ := st.ReportDevice(ctx, store.DiscoveredDevice{MAC: "de:ad:be:ef:00:34", Adapter: "gone"})
	if _, err := svc.Adopt(ctx, other.ID, ""); err == nil {
		t.Fatal("adopted through a missing adapter")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	devices := map[string]int64{}
	adoptions := map[string]int64{}
	var connected int64 = -1
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "beenfar.devices":
				for _, dp := range m.Data.(metricdata.Gauge[int64]).DataPoints {
					ad, _ := dp.Attributes.Value("adapter")
					state, _ := dp.Attributes.Value("state")
					locked, _ := dp.Attributes.Value("locked")
					devices[ad.AsString()+"/"+state.AsString()+"/"+locked.String()] += dp.Value
				}
			case "beenfar.adoptions":
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					ad, _ := dp.Attributes.Value("adapter")
					res, _ := dp.Attributes.Value("result")
					adoptions[ad.AsString()+"/"+res.AsString()] += dp.Value
				}
			case "beenfar.adapters.connected":
				connected = m.Data.(metricdata.Gauge[int64]).DataPoints[0].Value
			}
		}
	}
	wantDevices := map[string]int64{"fake/adopted/false": 1, "fake/pending/false": 1, "fake/pending/true": 1, "gone/pending/false": 1}
	if len(devices) != len(wantDevices) {
		t.Errorf("devices = %v, want %v", devices, wantDevices)
	}
	for k, v := range wantDevices {
		if devices[k] != v {
			t.Errorf("devices = %v, want %v", devices, wantDevices)
			break
		}
	}
	// Only adoptions that reached the registry count: the missing adapter's
	// is an error, the repeat never got that far.
	if adoptions["fake/ok"] != 1 || adoptions["gone/error"] != 1 || len(adoptions) != 2 {
		t.Errorf("adoptions = %v", adoptions)
	}
	if connected != 1 {
		t.Errorf("connected adapters = %d, want 1", connected)
	}
}
