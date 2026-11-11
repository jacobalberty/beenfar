package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"

	"connectrpc.com/connect/v2"

	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1/adminv1connect"
	"github.com/jacobalberty/beenfar/internal/auth"
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
	adminv1connect.AuthServiceLoginProcedure:  true,
	adminv1connect.AuthServiceLogoutProcedure: true,
}

// Procedures only admins may call. Handlers enforce finer rules (such as
// "admin or self") on the rest.
var adminProcedures = map[string]bool{
	adminv1connect.UserServiceCreateUserProcedure:      true,
	adminv1connect.UserServiceListUsersProcedure:       true,
	adminv1connect.UserServiceUpdateUserProcedure:      true,
	adminv1connect.UserServiceDeleteUserProcedure:      true,
	adminv1connect.AdapterServiceListAdaptersProcedure: true,
}

// authInterceptor authenticates every RPC before its payload is read and puts
// the principal into the context. Credentials are an "Authorization: Bearer"
// API token or, failing that, the session cookie.
func authInterceptor(svc *auth.Service) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if publicProcedures[spec.Procedure] {
				return next(ctx, spec, stream)
			}
			info, ok := connect.CallInfoForServerContext(ctx)
			if !ok {
				return connect.NewError(connect.CodeInternal, "missing call info")
			}
			p, err := authenticate(ctx, svc, info.RequestHeader())
			if err != nil {
				return err
			}
			if adminProcedures[spec.Procedure] && !p.IsAdmin() {
				return connect.NewError(connect.CodePermissionDenied, "admin role required")
			}
			ctx = auth.WithPrincipal(ctx, p)
			ctx = telemetry.WithAttrs(ctx,
				slog.String("enduser.id", p.User.ID),
				slog.String("enduser.name", p.User.Username),
				slog.String("auth.method", string(p.Method)),
			)
			return next(ctx, spec, stream)
		}
	}
}

func authenticate(ctx context.Context, svc *auth.Service, h *connect.Header) (*auth.Principal, error) {
	if authz := h.Get("Authorization"); authz != "" {
		scheme, token, ok := strings.Cut(authz, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return nil, connect.NewError(connect.CodeUnauthenticated, "unsupported authorization scheme; use Bearer")
		}
		p, err := svc.AuthenticateToken(ctx, strings.TrimSpace(token))
		if errors.Is(err, auth.ErrUnauthenticated) {
			return nil, connect.NewError(connect.CodeUnauthenticated, "invalid or expired token")
		}
		return p, err
	}

	cookie := sessionCookie(h)
	if cookie == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "credentials required")
	}
	// Cookies ride along on cross-site requests; refuse them unless the
	// request comes from our own origin (SameSite=Strict is the first line).
	if origin := h.Get("Origin"); origin != "" {
		ri := requestInfoFrom(ctx)
		u, err := url.Parse(origin)
		if err != nil || ri.host == "" || !strings.EqualFold(u.Host, ri.host) {
			return nil, connect.NewError(connect.CodePermissionDenied, "cross-origin request with session cookie")
		}
	}
	p, err := svc.AuthenticateSession(ctx, cookie)
	if errors.Is(err, auth.ErrUnauthenticated) {
		return nil, connect.NewError(connect.CodeUnauthenticated, "session expired; sign in again")
	}
	return p, err
}

func sessionCookie(h *connect.Header) string {
	for _, line := range h.Values("Cookie") {
		cookies, err := http.ParseCookie(line)
		if err != nil {
			continue
		}
		for _, c := range cookies {
			if c.Name == SessionCookieName {
				return c.Value
			}
		}
	}
	return ""
}

// requestInfo carries HTTP details the Connect call info does not expose
// for every transport (vanguard's REST path has no connecthttp.ServerInfo).
type requestInfo struct {
	host   string
	secure bool
}

type requestInfoKey struct{}

func withRequestInfo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ri := requestInfo{
			host:   r.Host,
			secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestInfoKey{}, ri)))
	})
}

func requestInfoFrom(ctx context.Context) requestInfo {
	ri, _ := ctx.Value(requestInfoKey{}).(requestInfo)
	return ri
}
