package server

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"

	adminv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1/adminv1connect"
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
	auth *auth.Service
}

func (h *authHandler) Login(ctx context.Context, req *adminv1.LoginRequest) (*adminv1.LoginResponse, error) {
	u, cookie, sess, err := h.auth.Login(ctx, req.GetUsername(), req.GetPassword(), requestInfoFrom(ctx).peer)
	if err != nil {
		if code, ok := codeOf(err); ok && code == connect.CodeUnauthenticated {
			return nil, connect.NewError(connect.CodeUnauthenticated, "invalid username or password")
		}
		return nil, err
	}
	setCookie(ctx, &http.Cookie{
		Value:   cookie,
		Expires: sess.ExpireTime,
		MaxAge:  int(time.Until(sess.ExpireTime).Seconds()),
	})
	return &adminv1.LoginResponse{User: apiconvgen.UserToProto(u)}, nil
}

func (h *authHandler) Logout(ctx context.Context, _ *adminv1.LogoutRequest) (*adminv1.LogoutResponse, error) {
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		if c := auth.SessionCookieValue(info.RequestHeader()); c != "" {
			if err := h.auth.Logout(ctx, c); err != nil {
				return nil, err
			}
		}
	}
	setCookie(ctx, &http.Cookie{MaxAge: -1})
	return &adminv1.LogoutResponse{}, nil
}

func (h *authHandler) GetCurrentUser(ctx context.Context, _ *adminv1.GetCurrentUserRequest) (*adminv1.GetCurrentUserResponse, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetCurrentUserResponse{User: apiconvgen.UserToProto(p.User)}, nil
}

// setCookie adds the session cookie to the response with beenfar's fixed
// attributes. Secure is set when the request arrived over TLS (directly or
// via a proxy that says so).
func setCookie(ctx context.Context, c *http.Cookie) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return
	}
	c.Name = SessionCookieName
	c.Path = "/"
	c.HttpOnly = true
	c.SameSite = http.SameSiteStrictMode
	c.Secure = requestInfoFrom(ctx).secure
	info.ResponseHeader().Add("Set-Cookie", c.String())
}

// --- AdapterService ---

type adapterHandler struct {
	adminv1connect.UnimplementedAdapterServiceHandler
	registry core.AdapterRegistry
}

func (h *adapterHandler) ListAdapters(ctx context.Context, _ *adminv1.ListAdaptersRequest) (*adminv1.ListAdaptersResponse, error) {
	list := core.SortedAdapters(ctx, h.registry)
	resp := &adminv1.ListAdaptersResponse{Adapters: make([]*adminv1.Adapter, 0, len(list))}
	for _, a := range list {
		resp.Adapters = append(resp.Adapters, apiconvgen.AdapterToProto(a))
	}
	return resp, nil
}
