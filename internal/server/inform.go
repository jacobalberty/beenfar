package server

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"

	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/telemetry"
	"github.com/jacobalberty/beenfar/service/adapter/unifi"
)

const (
	unifiAdapter = "unifi"
	// informHeaderSize is the fixed inform header length before the payload.
	informHeaderSize = 40
	maxInformSize    = 1 << 20
)

// informHandler is the interim UniFi inform endpoint. It records devices that
// ask to be adopted. Talking to adopted devices arrives with the adapter port
// in phase 4.
type informHandler struct {
	store store.Store
}

func (h *informHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInformSize))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "packet too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if len(body) < informHeaderSize {
		slog.InfoContext(ctx, "rejected inform: short packet", slog.Int("bytes", len(body)))
		http.Error(w, "short packet", http.StatusBadRequest)
		return
	}
	packet, err := unifi.NewInformBuilder(body)
	if err != nil {
		slog.InfoContext(ctx, "rejected inform: bad packet", slog.Any("error", err))
		http.Error(w, "bad packet", http.StatusBadRequest)
		return
	}

	ctx = telemetry.WithAttrs(ctx, slog.String("device.mac", packet.GetMac()))

	existing, err := h.store.GetDeviceByMAC(ctx, packet.GetMac())
	isNew := errors.Is(err, store.ErrNotFound)
	if err != nil && !isNew {
		writeStoreError(w, r, err)
		return
	}

	dev, err := h.store.ReportDevice(ctx, store.DiscoveredDevice{
		MAC:     packet.GetMac(),
		Adapter: unifiAdapter,
		IP:      remoteIP(r),
	})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	ctx = telemetry.WithAttrs(ctx, slog.String("device.id", dev.ID))

	if isNew {
		slog.InfoContext(ctx, "new adoption request")
	}
	if isNew || existing.State == store.DeviceStatePending {
		// Not adopted yet: UniFi devices keep retrying until they get a reply.
		http.Error(w, "", http.StatusNotFound)
		return
	}

	slog.WarnContext(ctx, "inform from adopted device not handled yet")
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, store.ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		slog.ErrorContext(r.Context(), "store error", slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
