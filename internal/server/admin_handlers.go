package server

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/jacobalberty/beenfar/adapters/unifi"
	adminv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1/adminv1connect"
	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/apiconv"
	apiconvgen "github.com/jacobalberty/beenfar/internal/apiconv/gen"
	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/store"
)

// SessionCookieName is the browser session cookie.
const SessionCookieName = auth.SessionCookieName

// principal returns the caller; the auth interceptor guarantees one on every
// non-public procedure.
func principal(ctx context.Context) (*auth.Principal, error) {
	p := auth.PrincipalFrom(ctx)
	if p == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "credentials required")
	}
	return p, nil
}

func requireSelfOrAdmin(p *auth.Principal, userID string) error {
	if p.User.ID == userID || p.IsAdmin() {
		return nil
	}
	return connect.NewError(connect.CodePermissionDenied, "admin role required")
}

// --- ApiTokenService ---

type apiTokenHandler struct {
	adminv1connect.UnimplementedApiTokenServiceHandler
	auth *auth.Service
}

func (h *apiTokenHandler) CreateApiToken(ctx context.Context, req *adminv1.CreateApiTokenRequest) (*adminv1.CreateApiTokenResponse, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var expire time.Time
	if e := req.GetApiToken().GetExpireTime(); e != nil {
		if err := e.CheckValid(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, "expire_time is not a valid timestamp")
		}
		expire = e.AsTime()
	}
	t, secret, err := h.auth.CreateAPIToken(ctx, p.User.ID, req.GetApiToken().GetName(), expire)
	if err != nil {
		return nil, err
	}
	return &adminv1.CreateApiTokenResponse{ApiToken: apiconvgen.APITokenToProto(t), Secret: secret}, nil
}

func (h *apiTokenHandler) ListApiTokens(ctx context.Context, _ *adminv1.ListApiTokensRequest) (*adminv1.ListApiTokensResponse, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	list, err := h.auth.ListAPITokens(ctx, p)
	if err != nil {
		return nil, err
	}
	resp := &adminv1.ListApiTokensResponse{ApiTokens: make([]*adminv1.ApiToken, 0, len(list))}
	for _, t := range list {
		resp.ApiTokens = append(resp.ApiTokens, apiconvgen.APITokenToProto(t))
	}
	return resp, nil
}

func (h *apiTokenHandler) DeleteApiToken(ctx context.Context, req *adminv1.DeleteApiTokenRequest) (*adminv1.DeleteApiTokenResponse, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.auth.DeleteAPIToken(ctx, p, req.GetId()); err != nil {
		return nil, err
	}
	return &adminv1.DeleteApiTokenResponse{}, nil
}

// --- UserService ---

type userHandler struct {
	adminv1connect.UnimplementedUserServiceHandler
	auth  *auth.Service
	store store.Store
}

func (h *userHandler) CreateUser(ctx context.Context, req *adminv1.CreateUserRequest) (*adminv1.CreateUserResponse, error) {
	u := req.GetUser()
	out, err := h.auth.CreateUser(ctx, u.GetUsername(), req.GetPassword(), u.GetAdmin())
	if err != nil {
		return nil, err
	}
	return &adminv1.CreateUserResponse{User: apiconvgen.UserToProto(out)}, nil
}

func (h *userHandler) GetUser(ctx context.Context, req *adminv1.GetUserRequest) (*adminv1.GetUserResponse, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireSelfOrAdmin(p, req.GetId()); err != nil {
		return nil, err
	}
	u, err := h.store.GetUser(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &adminv1.GetUserResponse{User: apiconvgen.UserToProto(u)}, nil
}

func (h *userHandler) ListUsers(ctx context.Context, _ *adminv1.ListUsersRequest) (*adminv1.ListUsersResponse, error) {
	list, err := h.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	resp := &adminv1.ListUsersResponse{Users: make([]*adminv1.User, 0, len(list))}
	for _, u := range list {
		resp.Users = append(resp.Users, apiconvgen.UserToProto(u))
	}
	return resp, nil
}

func (h *userHandler) UpdateUser(ctx context.Context, req *adminv1.UpdateUserRequest) (*adminv1.UpdateUserResponse, error) {
	u := req.GetUser()
	rev, err := apiconv.ParseEtag(u.GetEtag())
	if err != nil {
		return nil, err
	}
	out, err := h.auth.UpdateUser(ctx, store.User{ID: u.GetId(), Username: u.GetUsername(), Admin: u.GetAdmin(), Revision: rev})
	if err != nil {
		return nil, err
	}
	return &adminv1.UpdateUserResponse{User: apiconvgen.UserToProto(out)}, nil
}

func (h *userHandler) DeleteUser(ctx context.Context, req *adminv1.DeleteUserRequest) (*adminv1.DeleteUserResponse, error) {
	rev, err := apiconv.ParseEtag(req.GetEtag())
	if err != nil {
		return nil, err
	}
	if err := h.auth.DeleteUser(ctx, req.GetId(), rev); err != nil {
		return nil, err
	}
	return &adminv1.DeleteUserResponse{}, nil
}

func (h *userHandler) SetUserPassword(ctx context.Context, req *adminv1.SetUserPasswordRequest) (*adminv1.SetUserPasswordResponse, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireSelfOrAdmin(p, req.GetId()); err != nil {
		return nil, err
	}
	var current *string
	if p.User.ID == req.GetId() {
		c := req.GetCurrentPassword()
		current = &c
	}
	if err := h.auth.SetPassword(ctx, req.GetId(), req.GetPassword(), current); err != nil {
		return nil, err
	}
	return &adminv1.SetUserPasswordResponse{}, nil
}

// --- AuthService ---

type authHandler struct {
	adminv1connect.UnimplementedAuthServiceHandler
	auth    *auth.Service
	methods AuthMethods
}

func (h *authHandler) GetAuthConfig(context.Context, *adminv1.GetAuthConfigRequest) (*adminv1.GetAuthConfigResponse, error) {
	m := h.methods
	resp := &adminv1.GetAuthConfigResponse{
		PasswordLoginEnabled:  !m.DisablePasswordLogin,
		BasicEnabled:          m.Basic,
		BasicPasswordsEnabled: m.Basic && m.BasicPasswords,
	}
	for _, p := range m.OIDC.Providers() {
		resp.OidcProviders = append(resp.OidcProviders, &adminv1.OidcProvider{Id: p.ID, DisplayName: p.DisplayName, LoginUrl: p.LoginURL})
	}
	if m.Proxy {
		resp.Proxy = &adminv1.ProxyAuth{LogoutUrl: m.ProxyLogoutURL}
	}
	return resp, nil
}

func (h *authHandler) Login(ctx context.Context, req *adminv1.LoginRequest) (*adminv1.LoginResponse, error) {
	if h.methods.DisablePasswordLogin {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "password login is disabled; use your identity provider")
	}
	u, cookie, sess, err := h.auth.Login(ctx, req.GetUsername(), req.GetPassword(), requestInfoFrom(ctx).peer)
	if err != nil {
		if code, ok := codeOf(err); ok && code == connect.CodeUnauthenticated {
			return nil, connect.NewError(connect.CodeUnauthenticated, "invalid username or password")
		}
		return nil, err
	}
	setCookie(ctx, cookie, sess.ExpireTime)
	return &adminv1.LoginResponse{User: apiconvgen.UserToProto(u)}, nil
}

// Logout ends the beenfar session, if any, and says where to sign out
// upstream: the OIDC provider for an OIDC session, or the proxy when the
// caller has no session (proxy users never get one).
func (h *authHandler) Logout(ctx context.Context, _ *adminv1.LogoutRequest) (*adminv1.LogoutResponse, error) {
	resp := &adminv1.LogoutResponse{}
	cookie := ""
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		cookie = auth.SessionCookieValue(info.RequestHeader())
	}
	if cookie != "" {
		provider, err := h.auth.Logout(ctx, cookie)
		if err != nil {
			return nil, err
		}
		ri := requestInfoFrom(ctx)
		resp.RedirectUrl = h.methods.OIDC.LogoutURL(ctx, provider, ri.baseURL())
	} else if h.methods.Proxy {
		resp.RedirectUrl = h.methods.ProxyLogoutURL
	}
	setCookie(ctx, "", time.Time{}) // clears it
	return resp, nil
}

var authMethodToProto = map[auth.Method]adminv1.AuthMethod{
	auth.MethodToken:   adminv1.AuthMethod_AUTH_METHOD_TOKEN,
	auth.MethodSession: adminv1.AuthMethod_AUTH_METHOD_SESSION,
	auth.MethodBasic:   adminv1.AuthMethod_AUTH_METHOD_BASIC,
	auth.MethodProxy:   adminv1.AuthMethod_AUTH_METHOD_PROXY,
	auth.MethodOIDC:    adminv1.AuthMethod_AUTH_METHOD_OIDC,
}

func (h *authHandler) GetCurrentUser(ctx context.Context, _ *adminv1.GetCurrentUserRequest) (*adminv1.GetCurrentUserResponse, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetCurrentUserResponse{User: apiconvgen.UserToProto(p.User), Method: authMethodToProto[p.Method]}, nil
}

// setCookie sets (or, with an empty value, clears) the session cookie on
// the response. Secure is set when the request arrived over TLS, directly or
// via a proxy that says so.
func setCookie(ctx context.Context, value string, expires time.Time) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return
	}
	c := auth.NewSessionCookie(value, expires, requestInfoFrom(ctx).secure)
	info.ResponseHeader().Add("Set-Cookie", c.String())
}

// --- AdapterService ---

type adapterHandler struct {
	adminv1connect.UnimplementedAdapterServiceHandler
	registry core.AdapterRegistry
	store    store.Store
}

func (h *adapterHandler) ImportUnifiDeviceKeys(ctx context.Context, req *adminv1.ImportUnifiDeviceKeysRequest) (*adminv1.ImportUnifiDeviceKeysResponse, error) {
	in := make([]unifi.KeyImport, 0, len(req.GetDevices()))
	for _, d := range req.GetDevices() {
		in = append(in, unifi.KeyImport{MAC: d.GetMac(), AuthKey: d.GetAuthkey(), GCM: d.GetGcm()})
	}
	n, err := unifi.ImportKeys(ctx, adapterhost.NewHost(unifi.Name, h.store), in)
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "imported UniFi device keys", slog.Int("devices", n))
	return &adminv1.ImportUnifiDeviceKeysResponse{Imported: int32(n)}, nil
}

func (h *adapterHandler) ListAdapters(ctx context.Context, _ *adminv1.ListAdaptersRequest) (*adminv1.ListAdaptersResponse, error) {
	list := core.SortedAdapters(ctx, h.registry)
	resp := &adminv1.ListAdaptersResponse{Adapters: make([]*adminv1.Adapter, 0, len(list))}
	for _, a := range list {
		resp.Adapters = append(resp.Adapters, apiconvgen.AdapterToProto(a))
	}
	return resp, nil
}
