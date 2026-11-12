package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"runtime/debug"
	"strings"

	"connectrpc.com/connect/v2"

	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/adapter/v1/adapterv1connect"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1/adminv1connect"
	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

// errorInterceptor turns domain errors returned by handlers into Connect
// errors. Expected errors keep their message; anything else is logged and
// reported as a bare INTERNAL so implementation details never leak.
func errorInterceptor(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		err := next(ctx, spec, stream)
		if err == nil {
			return nil
		}
		if _, ok := errors.AsType[*connect.Error](err); ok {
			return err
		}
		if code, ok := codeOf(err); ok {
			return connect.NewError(code, err.Error()).WithCause(err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return err // connect reports CANCELED / DEADLINE_EXCEEDED
		}
		slog.ErrorContext(ctx, "rpc failed", slog.String("procedure", spec.Procedure), slog.Any("error", err))
		return connect.NewError(connect.CodeInternal, "internal error").WithCause(err)
	}
}

func codeOf(err error) (connect.Code, bool) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return connect.CodeNotFound, true
	case errors.Is(err, store.ErrInvalid):
		return connect.CodeInvalidArgument, true
	case errors.Is(err, store.ErrConflict):
		return connect.CodeAlreadyExists, true
	case errors.Is(err, store.ErrStale):
		return connect.CodeAborted, true
	case errors.Is(err, store.ErrFailedPrecondition):
		return connect.CodeFailedPrecondition, true
	case errors.Is(err, auth.ErrUnauthenticated):
		return connect.CodeUnauthenticated, true
	case errors.Is(err, auth.ErrRateLimited):
		return connect.CodeResourceExhausted, true
	case errors.Is(err, auth.ErrPermissionDenied):
		return connect.CodePermissionDenied, true
	}
	return 0, false
}

// recoverInterceptor turns a handler panic into INTERNAL.
func recoverInterceptor(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (err error) {
		defer func() {
			if r := recover(); r != nil {
				slog.ErrorContext(ctx, "rpc panicked",
					slog.String("procedure", spec.Procedure),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
				err = connect.NewError(connect.CodeInternal, "internal error")
			}
		}()
		return next(ctx, spec, stream)
	}
}

// Procedures callable without credentials.
var publicProcedures = map[string]bool{
	adminv1connect.AuthServiceLoginProcedure:         true,
	adminv1connect.AuthServiceLogoutProcedure:        true,
	adminv1connect.AuthServiceGetAuthConfigProcedure: true,
}

// Procedures only admins may call. Handlers enforce finer rules (such as
// "admin or self") on the rest.
var adminProcedures = map[string]bool{
	adminv1connect.UserServiceCreateUserProcedure:               true,
	adminv1connect.UserServiceListUsersProcedure:                true,
	adminv1connect.UserServiceUpdateUserProcedure:               true,
	adminv1connect.UserServiceDeleteUserProcedure:               true,
	adminv1connect.AdapterServiceListAdaptersProcedure:          true,
	adminv1connect.AdapterServiceImportUnifiDeviceKeysProcedure: true,
}

// authInterceptor authenticates every RPC before its payload is read and puts
// the principal into the context.
//
// The adapter API is authenticated apart from the rest: its callers are
// adapters, not users, and only adapter tokens are accepted there.
func authInterceptor(authn auth.Authenticator, adapters *auth.Service) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if publicProcedures[spec.Procedure] {
				return next(ctx, spec, stream)
			}
			info, ok := connect.CallInfoForServerContext(ctx)
			if !ok {
				return connect.NewError(connect.CodeInternal, "missing call info")
			}
			h := info.RequestHeader()
			if strings.HasPrefix(spec.Procedure, "/"+adapterv1connect.AdapterHostServiceName+"/") {
				token, ok := auth.AdapterBearer(h)
				if !ok {
					return fmt.Errorf("%w: adapter token required", auth.ErrUnauthenticated)
				}
				name, err := adapters.AuthenticateAdapterToken(ctx, token)
				if errors.Is(err, auth.ErrUnauthenticated) {
					return fmt.Errorf("%w: invalid adapter token", auth.ErrUnauthenticated)
				}
				if err != nil {
					return err
				}
				return next(adapterhost.WithAdapter(ctx, name), spec, stream)
			}
			ri := requestInfoFrom(ctx)
			p, err := authn.Authenticate(ctx, auth.Request{Header: h, Peer: ri.peer})
			if err != nil {
				return err
			}
			// Ambient credentials ride along on cross-site requests; refuse
			// them unless the request comes from our own origin.
			if p.Ambient && !sameOrigin(h.Get("Origin"), ri.host) {
				return connect.NewError(connect.CodePermissionDenied, "cross-origin request with browser credentials")
			}
			if adminProcedures[spec.Procedure] && !p.IsAdmin() {
				return connect.NewError(connect.CodePermissionDenied, "admin role required")
			}
			ctx = auth.WithPrincipal(ctx, p)
			ctx = core.WithWriter(ctx, fieldWriterOf(p))
			ctx = telemetry.WithAttrs(ctx,
				slog.String("enduser.id", p.User.ID),
				slog.String("enduser.name", p.User.Username),
				slog.String("auth.method", string(p.Method)),
			)
			return next(ctx, spec, stream)
		}
	}
}

// fieldWriterOf says how the caller is recorded as the writer of fields: an
// API token by its name, anyone else by user name.
func fieldWriterOf(p *auth.Principal) store.FieldWriter {
	if p.Method == auth.MethodToken && p.TokenName != "" {
		return store.FieldWriter{Writer: p.TokenName, Kind: store.FieldWriterAPIToken}
	}
	return store.FieldWriter{Writer: p.User.Username, Kind: store.FieldWriterUser}
}

// sameOrigin reports whether an Origin header (absent for same-origin GETs
// and non-browser clients) names host.
func sameOrigin(origin, host string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && host != "" && strings.EqualFold(u.Host, host)
}

// requestInfo carries HTTP details the Connect call info does not expose
// for every transport (vanguard's REST path has no connecthttp.ServerInfo).
type requestInfo struct {
	host   string
	secure bool
	peer   netip.Addr // TCP peer; for trusted-proxy checks
}

type requestInfoKey struct{}

func withRequestInfo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, _ := netip.ParseAddrPort(r.RemoteAddr)
		ri := requestInfo{
			peer:   peer.Addr().Unmap(),
			host:   r.Host,
			secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestInfoKey{}, ri)))
	})
}

// baseURL is the scheme and host the client used.
func (ri requestInfo) baseURL() string {
	if ri.secure {
		return "https://" + ri.host
	}
	return "http://" + ri.host
}

func requestInfoFrom(ctx context.Context) requestInfo {
	ri, _ := ctx.Value(requestInfoKey{}).(requestInfo)
	return ri
}
