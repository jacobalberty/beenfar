// Package server wires beenfar's HTTP surface on a plain http.ServeMux.
//
// The API is served three ways from one connect.Server, so every transport
// shares the same interceptors (telemetry, error mapping, auth):
//
//   - Connect, gRPC and gRPC-Web at the protobuf service paths, e.g.
//     /beenfar.config.v1.WifiNetworkService/CreateWifiNetwork (connecthttp);
//   - REST under /api/v1/..., from the google.api.http annotations (vanguard);
//   - the OpenAPI description of the REST binding at /openapi.json.
package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/otelconnect"
	"connectrpc.com/vanguard"

	"github.com/jacobalberty/beenfar/api"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1/adminv1connect"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

// maxMessageBytes bounds request messages on every transport.
const maxMessageBytes = 4 << 20

// Options configures New.
type Options struct {
	Store     store.Store
	Telemetry *telemetry.Telemetry
	Auth      *auth.Service
	Core      *core.Services
}

// New returns the root handler.
func New(opts Options) (http.Handler, error) {
	otelInterceptor, err := otelconnect.NewServerInterceptor(
		otelconnect.WithTracerProvider(opts.Telemetry.TracerProvider),
		otelconnect.WithMeterProvider(opts.Telemetry.MeterProvider),
		otelconnect.WithPropagator(opts.Telemetry.TextMapPropagator()),
	)
	if err != nil {
		return nil, fmt.Errorf("otelconnect: %w", err)
	}

	// Outermost first: telemetry sees the final status code, errors are mapped
	// before that, and auth runs before any payload is decoded.
	rpc := connect.NewServer(otelInterceptor, errorInterceptor, recoverInterceptor, authInterceptor(opts.Auth))
	configv1connect.RegisterWifiNetworkServiceHandler(rpc, &wifiHandler{svc: opts.Core.Wifi})
	configv1connect.RegisterNetworkServiceHandler(rpc, &networkHandler{svc: opts.Core.Networks})
	configv1connect.RegisterDeviceServiceHandler(rpc, &deviceHandler{svc: opts.Core.Devices})
	adminv1connect.RegisterApiTokenServiceHandler(rpc, &apiTokenHandler{auth: opts.Auth})
	adminv1connect.RegisterUserServiceHandler(rpc, &userHandler{auth: opts.Auth, store: opts.Store})
	adminv1connect.RegisterAuthServiceHandler(rpc, &authHandler{auth: opts.Auth})
	adminv1connect.RegisterAdapterServiceHandler(rpc, &adapterHandler{registry: opts.Core.Adapters})

	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc, connecthttp.WithReadMaxBytes(maxMessageBytes))

	// vanguard installs a catch-all, so give it its own mux scoped to /api/.
	rest := http.NewServeMux()
	if err := vanguard.Mount(rest, rpc, vanguard.WithMaxReadBytes(maxMessageBytes)); err != nil {
		return nil, fmt.Errorf("vanguard: %w", err)
	}
	mux.Handle("/api/", rest)

	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(api.OpenAPIJSON)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := opts.Store.Ping(r.Context()); err != nil {
			slog.WarnContext(r.Context(), "health check failed", slog.Any("error", err))
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})

	// TODO(phase 4): the UniFi inform endpoint moves into adapters/unifi and
	// is mounted through the adapter interface.
	mux.Handle("POST /inform", &informHandler{store: opts.Store})

	// withRequestInfo goes outside the telemetry middleware, which reads the
	// matched route from the request it hands to the mux.
	return withRequestInfo(opts.Telemetry.Middleware(mux)), nil
}
