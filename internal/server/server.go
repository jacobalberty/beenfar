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
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/adapter/v1/adapterv1connect"
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
	// Authenticator decides who is calling. Nil means API tokens and the
	// session cookie (Auth.BearerToken, Auth.SessionCookie).
	Authenticator auth.Authenticator
	// AuthMethods describes the configured sign-in methods for
	// GetAuthConfig, Login and Logout, and mounts the OIDC routes.
	AuthMethods AuthMethods
	Core        *core.Services
	// AdapterHost serves the adapter API to external adapters. Nil leaves the
	// API unregistered.
	AdapterHost adapterv1connect.AdapterHostServiceHandler
	// AdapterHTTP are the device-facing routes of built-in adapters (such as
	// UniFi's POST /inform), keyed by ServeMux pattern. They authenticate
	// the devices themselves, so they sit outside the API's auth.
	AdapterHTTP map[string]http.Handler
}

// AuthMethods are the sign-in methods beyond API tokens and password login.
// The authenticators themselves are in Options.Authenticator; this is what
// the API reports and the HTTP routes they need.
type AuthMethods struct {
	DisablePasswordLogin bool
	Basic                bool
	BasicPasswords       bool
	Proxy                bool
	ProxyLogoutURL       string
	OIDC                 *auth.OIDC // nil when not configured
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

	authn := opts.Authenticator
	if authn == nil {
		authn = auth.Chain{opts.Auth.BearerToken(), opts.Auth.SessionCookie()}
	}

	// Outermost first: telemetry sees the final status code, errors are mapped
	// before that, and auth runs before any payload is decoded.
	rpc := connect.NewServer(otelInterceptor, errorInterceptor, recoverInterceptor, authInterceptor(authn, opts.Auth))
	configv1connect.RegisterWifiNetworkServiceHandler(rpc, &wifiHandler{svc: opts.Core.Wifi})
	configv1connect.RegisterNetworkServiceHandler(rpc, &networkHandler{svc: opts.Core.Networks})
	configv1connect.RegisterDeviceServiceHandler(rpc, &deviceHandler{svc: opts.Core.Devices})
	adminv1connect.RegisterApiTokenServiceHandler(rpc, &apiTokenHandler{auth: opts.Auth})
	adminv1connect.RegisterUserServiceHandler(rpc, &userHandler{auth: opts.Auth, store: opts.Store})
	adminv1connect.RegisterAuthServiceHandler(rpc, &authHandler{auth: opts.Auth, methods: opts.AuthMethods})
	adminv1connect.RegisterAdapterServiceHandler(rpc, &adapterHandler{registry: opts.Core.Adapters, store: opts.Store})
	if opts.AdapterHost != nil {
		adapterv1connect.RegisterAdapterHostServiceHandler(rpc, opts.AdapterHost)
	}

	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc, connecthttp.WithReadMaxBytes(maxMessageBytes))

	// vanguard installs a catch-all, so give it its own mux scoped to /api/.
	rest := http.NewServeMux()
	if err := vanguard.Mount(rest, rpc, vanguard.WithMaxReadBytes(maxMessageBytes)); err != nil {
		return nil, fmt.Errorf("vanguard: %w", err)
	}
	mux.Handle("/api/", rest)

	if opts.AuthMethods.OIDC != nil {
		mux.Handle("/auth/oidc/", opts.AuthMethods.OIDC.Handler())
	}

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

	for pattern, h := range opts.AdapterHTTP {
		mux.Handle(pattern, h)
	}

	// withRequestInfo goes outside the telemetry middleware, which reads the
	// matched route from the request it hands to the mux.
	return withRequestInfo(opts.Telemetry.Middleware(mux)), nil
}
