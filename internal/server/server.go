// Package server wires beenfar's HTTP surface on a plain http.ServeMux.
package server

import (
	"log/slog"
	"net/http"

	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

// Options configures New.
type Options struct {
	Store     store.Store
	Telemetry *telemetry.Telemetry
}

// New returns the root handler.
func New(opts Options) http.Handler {
	mux := http.NewServeMux()

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

	return opts.Telemetry.Middleware(mux)
}
