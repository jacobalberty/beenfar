package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	adminv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/admin/v1/adminv1connect"
	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
	"github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1/configv1connect"
	"github.com/jacobalberty/beenfar/internal/server"
	"github.com/jacobalberty/beenfar/internal/store"
)

var noExpiry time.Time

// apiFixture is a running server with an admin user and API token.
type apiFixture struct {
	fixture
	srv        *httptest.Server
	adminID    string
	adminToken string
}

func newAPIFixture(t *testing.T) apiFixture {
	t.Helper()
	f := newFixture(t)
	srv := httptest.NewServer(f.handler)
	t.Cleanup(srv.Close)
	u, err := f.auth.CreateUser(t.Context(), "admin", "correct horse", true)
	if err != nil {
		t.Fatal(err)
	}
	tok := f.token(t, u.ID)
	return apiFixture{fixture: f, srv: srv, adminID: u.ID, adminToken: tok}
}

func (f fixture) token(t *testing.T, userID string) string {
	t.Helper()
	_, secret, err := f.auth.CreateAPIToken(t.Context(), userID, "test", noExpiry)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

// client returns a Connect client that authenticates with token (if any).
func (f apiFixture) client(token string, opts ...connecthttp.Option) *connect.Client {
	var interceptors []connect.ClientInterceptor
	if token != "" {
		interceptors = append(interceptors, func(next connect.ClientFunc) connect.ClientFunc {
			return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
				if info, ok := connect.CallInfoForClientContext(ctx); ok {
					info.RequestHeader().Set("Authorization", "Bearer "+token)
				}
				return next(ctx, spec)
			}
		})
	}
	return connect.NewClient(connecthttp.NewTransport(f.srv.Client(), f.srv.URL, opts...), interceptors...)
}

// rest sends a REST request and returns the status and body.
func (f apiFixture) rest(t *testing.T, method, path, token, body string, header ...string) (int, []byte, http.Header) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, f.srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Add(header[i], header[i+1])
	}
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b, resp.Header
}

func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("code = %v (%v), want %v", got, err, want)
	}
}

func wpaNetwork(ssid string) *configv1.WifiNetwork {
	return &configv1.WifiNetwork{
		Ssid:        ssid,
		Security:    configv1.WifiSecurity_WIFI_SECURITY_WPA_PERSONAL,
		SecurityKey: "hunter2hunter2",
		ApGroups:    []string{"upstairs"},
	}
}

func TestUnauthenticated(t *testing.T) {
	f := newAPIFixture(t)

	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(""))
	_, err := wifi.ListWifiNetworks(t.Context(), &configv1.ListWifiNetworksRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)

	bad := configv1connect.NewWifiNetworkServiceClient(f.client("bf_aaaaaaaaaaaa_nope"))
	_, err = bad.ListWifiNetworks(t.Context(), &configv1.ListWifiNetworksRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)

	for _, path := range []string{"/api/v1/wifi-networks", "/api/v1/devices", "/api/v1/users", "/api/v1/auth/me"} {
		if code, body, _ := f.rest(t, http.MethodGet, path, "", ""); code != http.StatusUnauthorized {
			t.Errorf("GET %s without token = %d %s, want 401", path, code, body)
		}
	}
	if code, _, _ := f.rest(t, http.MethodGet, "/api/v1/wifi-networks", "", "", "Authorization", "Basic YTpi"); code != http.StatusUnauthorized {
		t.Errorf("basic auth = %d, want 401", code)
	}

	// Health and the API description stay public.
	for _, path := range []string{"/healthz", "/openapi.json"} {
		if code, _, _ := f.rest(t, http.MethodGet, path, "", ""); code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, code)
		}
	}
}

// The same data is served over Connect (binary and JSON), gRPC and REST.
func TestConnectAndRESTAgree(t *testing.T) {
	f := newAPIFixture(t)
	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken))
	created, err := wifi.CreateWifiNetwork(t.Context(), &configv1.CreateWifiNetworkRequest{WifiNetwork: wpaNetwork("home")})
	if err != nil {
		t.Fatal(err)
	}

	want, err := wifi.ListWifiNetworks(t.Context(), &configv1.ListWifiNetworksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(want.GetWifiNetworks()) != 1 || !proto.Equal(want.GetWifiNetworks()[0], created.GetWifiNetwork()) {
		t.Fatalf("list = %v, created = %v", want, created)
	}

	for name, opts := range map[string][]connecthttp.Option{
		"grpc":         {connecthttp.WithGRPC()},
		"grpc-web":     {connecthttp.WithGRPCWeb()},
		"connect-json": {connecthttp.WithProtoJSON()},
	} {
		got, err := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken, opts...)).
			ListWifiNetworks(t.Context(), &configv1.ListWifiNetworksRequest{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !proto.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}

	code, body, _ := f.rest(t, http.MethodGet, "/api/v1/wifi-networks", f.adminToken, "")
	if code != http.StatusOK {
		t.Fatalf("REST list = %d %s", code, body)
	}
	var got configv1.ListWifiNetworksResponse
	if err := protojson.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(&got, want) {
		t.Errorf("REST list = %v, want %v", &got, want)
	}

	// And REST writes are visible over Connect.
	code, body, _ = f.rest(t, http.MethodPost, "/api/v1/wifi-networks", f.adminToken,
		`{"ssid":"guest","security":"WIFI_SECURITY_OPEN","guest":true}`)
	if code != http.StatusOK {
		t.Fatalf("REST create = %d %s", code, body)
	}
	var guest configv1.WifiNetwork
	if err := protojson.Unmarshal(body, &guest); err != nil {
		t.Fatal(err)
	}
	res, err := wifi.GetWifiNetwork(t.Context(), &configv1.GetWifiNetworkRequest{Id: guest.GetId()})
	if err != nil || !res.GetWifiNetwork().GetGuest() || res.GetWifiNetwork().GetBand() != configv1.WifiBand_WIFI_BAND_BOTH {
		t.Fatalf("get REST-created network = %v, %v", res, err)
	}
}

func TestWifiNetworkLifecycle(t *testing.T) {
	f := newAPIFixture(t)
	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken))
	ctx := t.Context()

	res, err := wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: wpaNetwork("home")})
	if err != nil {
		t.Fatal(err)
	}
	w := res.GetWifiNetwork()
	if w.GetId() == "" || w.GetEtag() == "" || w.GetCreateTime() == nil {
		t.Fatalf("missing output fields: %v", w)
	}

	_, err = wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: wpaNetwork("home")})
	wantCode(t, err, connect.CodeAlreadyExists)

	short := wpaNetwork("short")
	short.SecurityKey = "1234"
	_, err = wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: short})
	wantCode(t, err, connect.CodeInvalidArgument)

	// Rename keeps the id; the etag changes.
	renamed := proto.CloneOf(w)
	renamed.Ssid = "home-renamed"
	upd, err := wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{WifiNetwork: renamed})
	if err != nil {
		t.Fatal(err)
	}
	if upd.GetWifiNetwork().GetId() != w.GetId() || upd.GetWifiNetwork().GetEtag() == w.GetEtag() {
		t.Fatalf("update = %v", upd)
	}

	// The old etag is stale for both update and delete.
	_, err = wifi.UpdateWifiNetwork(ctx, &configv1.UpdateWifiNetworkRequest{WifiNetwork: renamed})
	wantCode(t, err, connect.CodeAborted)
	_, err = wifi.DeleteWifiNetwork(ctx, &configv1.DeleteWifiNetworkRequest{Id: w.GetId(), Etag: w.GetEtag()})
	wantCode(t, err, connect.CodeAborted)
	_, err = wifi.DeleteWifiNetwork(ctx, &configv1.DeleteWifiNetworkRequest{Id: w.GetId(), Etag: "not-a-number"})
	wantCode(t, err, connect.CodeInvalidArgument)

	if _, err := wifi.DeleteWifiNetwork(ctx, &configv1.DeleteWifiNetworkRequest{Id: w.GetId(), Etag: upd.GetWifiNetwork().GetEtag()}); err != nil {
		t.Fatal(err)
	}
	_, err = wifi.GetWifiNetwork(ctx, &configv1.GetWifiNetworkRequest{Id: w.GetId()})
	wantCode(t, err, connect.CodeNotFound)

	// REST maps the codes to HTTP statuses.
	code, _, _ := f.rest(t, http.MethodGet, "/api/v1/wifi-networks/"+w.GetId(), f.adminToken, "")
	if code != http.StatusNotFound {
		t.Errorf("REST get deleted = %d, want 404", code)
	}
}

func TestNetworkReferences(t *testing.T) {
	f := newAPIFixture(t)
	nets := configv1connect.NewNetworkServiceClient(f.client(f.adminToken))
	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken))
	ctx := t.Context()

	n, err := nets.CreateNetwork(ctx, &configv1.CreateNetworkRequest{Network: &configv1.Network{
		Name: "iot", VlanId: 20, Subnet: "10.0.20.1/24",
		DhcpEnabled: true, DhcpStart: "10.0.20.100", DhcpStop: "10.0.20.200",
	}})
	if err != nil {
		t.Fatal(err)
	}

	for name, bad := range map[string]*configv1.Network{
		"vlan taken":       {Name: "iot2", VlanId: 20},
		"vlan range":       {Name: "x", VlanId: 4095},
		"network address":  {Name: "x", Subnet: "10.0.30.0/24"},
		"dhcp outside":     {Name: "x", Subnet: "10.0.30.1/24", DhcpEnabled: true, DhcpStart: "10.0.31.1", DhcpStop: "10.0.31.9"},
		"dhcp needs range": {Name: "x", Subnet: "10.0.30.1/24", DhcpEnabled: true},
	} {
		_, err := nets.CreateNetwork(ctx, &configv1.CreateNetworkRequest{Network: bad})
		want := connect.CodeInvalidArgument
		if name == "vlan taken" {
			want = connect.CodeAlreadyExists
		}
		if connect.CodeOf(err) != want {
			t.Errorf("%s: code = %v (%v), want %v", name, connect.CodeOf(err), err, want)
		}
	}

	w := wpaNetwork("iot-wifi")
	w.NetworkId = "missing"
	_, err = wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: w})
	wantCode(t, err, connect.CodeInvalidArgument)

	w.NetworkId = n.GetNetwork().GetId()
	created, err := wifi.CreateWifiNetwork(ctx, &configv1.CreateWifiNetworkRequest{WifiNetwork: w})
	if err != nil {
		t.Fatal(err)
	}
	_, err = nets.DeleteNetwork(ctx, &configv1.DeleteNetworkRequest{Id: n.GetNetwork().GetId()})
	wantCode(t, err, connect.CodeFailedPrecondition)

	if _, err := wifi.DeleteWifiNetwork(ctx, &configv1.DeleteWifiNetworkRequest{Id: created.GetWifiNetwork().GetId()}); err != nil {
		t.Fatal(err)
	}
	if _, err := nets.DeleteNetwork(ctx, &configv1.DeleteNetworkRequest{Id: n.GetNetwork().GetId(), Etag: n.GetNetwork().GetEtag()}); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceAdoption(t *testing.T) {
	f := newAPIFixture(t)
	devices := configv1connect.NewDeviceServiceClient(f.client(f.adminToken))
	ctx := t.Context()

	d, err := f.store.ReportDevice(ctx, store.DiscoveredDevice{MAC: "DE-AD-BE-EF-00-01", Adapter: "unifi", Model: "U6-Lite"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := devices.ListPendingDevices(ctx, &configv1.ListPendingDevicesRequest{})
	if err != nil || len(pending.GetDevices()) != 1 || pending.GetDevices()[0].GetState() != configv1.DeviceState_DEVICE_STATE_PENDING {
		t.Fatalf("pending = %v, %v", pending, err)
	}

	// Adopt by MAC over REST, in a different notation than reported.
	code, body, _ := f.rest(t, http.MethodPost, "/api/v1/devices:adopt", f.adminToken, `{"mac":"deadbeef0001"}`)
	if code != http.StatusOK {
		t.Fatalf("REST adopt = %d %s", code, body)
	}
	var adopted configv1.Device // REST returns the resource itself (response_body)
	if err := protojson.Unmarshal(body, &adopted); err != nil {
		t.Fatal(err)
	}
	if adopted.GetId() != d.ID || adopted.GetState() != configv1.DeviceState_DEVICE_STATE_ADOPTED {
		t.Fatalf("adopted = %v", &adopted)
	}

	_, err = devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Id{Id: d.ID}})
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{Device: &configv1.AdoptDeviceRequest_Mac{Mac: "00:00:00:00:00:00"}})
	wantCode(t, err, connect.CodeNotFound)
	_, err = devices.AdoptDevice(ctx, &configv1.AdoptDeviceRequest{})
	wantCode(t, err, connect.CodeInvalidArgument)

	all, err := devices.ListDevices(ctx, &configv1.ListDevicesRequest{State: configv1.DeviceState_DEVICE_STATE_ADOPTED})
	if err != nil || len(all.GetDevices()) != 1 || all.GetDevices()[0].GetModel() != "U6-Lite" {
		t.Fatalf("adopted list = %v, %v", all, err)
	}

	if _, err := devices.ForgetDevice(ctx, &configv1.ForgetDeviceRequest{Id: d.ID}); err != nil {
		t.Fatal(err)
	}
	_, err = devices.GetDevice(ctx, &configv1.GetDeviceRequest{Id: d.ID})
	wantCode(t, err, connect.CodeNotFound)
}

func TestSessionLogin(t *testing.T) {
	f := newAPIFixture(t)

	code, _, _ := f.rest(t, http.MethodPost, "/api/v1/auth/login", "", `{"username":"admin","password":"wrong password"}`)
	if code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", code)
	}
	code, _, _ = f.rest(t, http.MethodPost, "/api/v1/auth/login", "", `{"username":"nobody","password":"wrong password"}`)
	if code != http.StatusUnauthorized {
		t.Fatalf("unknown user login = %d, want 401", code)
	}

	code, body, hdr := f.rest(t, http.MethodPost, "/api/v1/auth/login", "", `{"username":"admin","password":"correct horse"}`)
	if code != http.StatusOK {
		t.Fatalf("login = %d %s", code, body)
	}
	cookies, err := http.ParseSetCookie(hdr.Get("Set-Cookie"))
	if err != nil {
		t.Fatal(err)
	}
	if cookies.Name != server.SessionCookieName || !cookies.HttpOnly || cookies.SameSite != http.SameSiteStrictMode || cookies.Value == "" {
		t.Fatalf("cookie = %+v", cookies)
	}
	cookie := cookies.Name + "=" + cookies.Value

	code, body, _ = f.rest(t, http.MethodGet, "/api/v1/auth/me", "", "", "Cookie", cookie)
	if code != http.StatusOK || !strings.Contains(string(body), `"admin"`) {
		t.Fatalf("me = %d %s", code, body)
	}

	// The cookie works over Connect too, from the same origin only.
	host := strings.TrimPrefix(f.srv.URL, "http://")
	code, _, _ = f.rest(t, http.MethodGet, "/api/v1/wifi-networks", "", "", "Cookie", cookie, "Origin", "http://"+host)
	if code != http.StatusOK {
		t.Errorf("same-origin cookie request = %d", code)
	}
	code, _, _ = f.rest(t, http.MethodGet, "/api/v1/wifi-networks", "", "", "Cookie", cookie, "Origin", "https://evil.example")
	if code != http.StatusForbidden {
		t.Errorf("cross-origin cookie request = %d, want 403", code)
	}

	code, _, hdr = f.rest(t, http.MethodPost, "/api/v1/auth/logout", "", `{}`, "Cookie", cookie)
	if code != http.StatusOK || !strings.Contains(hdr.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout = %d %v", code, hdr)
	}
	code, _, _ = f.rest(t, http.MethodGet, "/api/v1/auth/me", "", "", "Cookie", cookie)
	if code != http.StatusUnauthorized {
		t.Errorf("me after logout = %d, want 401", code)
	}
}

func TestUserAndTokenPermissions(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()

	adminUsers := adminv1connect.NewUserServiceClient(f.client(f.adminToken))
	created, err := adminUsers.CreateUser(ctx, &adminv1.CreateUserRequest{
		User: &adminv1.User{Username: "operator"}, Password: "operator-pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	op := created.GetUser()

	// The operator gets a token through the API, like the UI would.
	opTokens := adminv1connect.NewApiTokenServiceClient(f.client(f.token(t, op.GetId())))
	tokRes, err := opTokens.CreateApiToken(ctx, &adminv1.CreateApiTokenRequest{ApiToken: &adminv1.ApiToken{Name: "terraform"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tokRes.GetSecret(), tokRes.GetApiToken().GetPrefix()+"_") {
		t.Errorf("secret %q does not start with prefix %q", tokRes.GetSecret(), tokRes.GetApiToken().GetPrefix())
	}
	opToken := tokRes.GetSecret()
	opUsers := adminv1connect.NewUserServiceClient(f.client(opToken))

	// Config APIs are open to any user; user management is not.
	if _, err := configv1connect.NewWifiNetworkServiceClient(f.client(opToken)).
		ListWifiNetworks(ctx, &configv1.ListWifiNetworksRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err = opUsers.ListUsers(ctx, &adminv1.ListUsersRequest{})
	wantCode(t, err, connect.CodePermissionDenied)
	_, err = opUsers.GetUser(ctx, &adminv1.GetUserRequest{Id: f.adminID})
	wantCode(t, err, connect.CodePermissionDenied)
	_, err = adminv1connect.NewAdapterServiceClient(f.client(opToken)).ListAdapters(ctx, &adminv1.ListAdaptersRequest{})
	wantCode(t, err, connect.CodePermissionDenied)
	if _, err := opUsers.GetUser(ctx, &adminv1.GetUserRequest{Id: op.GetId()}); err != nil {
		t.Errorf("get self: %v", err)
	}

	// Users see only their own tokens; admins see all.
	mine, err := adminv1connect.NewApiTokenServiceClient(f.client(opToken)).ListApiTokens(ctx, &adminv1.ListApiTokensRequest{})
	if err != nil || len(mine.GetApiTokens()) != 2 {
		t.Fatalf("operator tokens = %v, %v", mine, err)
	}
	adminTokens := adminv1connect.NewApiTokenServiceClient(f.client(f.adminToken))
	all, err := adminTokens.ListApiTokens(ctx, &adminv1.ListApiTokensRequest{})
	if err != nil || len(all.GetApiTokens()) != 3 {
		t.Fatalf("all tokens = %v, %v", all, err)
	}
	adminTokenID := ""
	for _, tok := range all.GetApiTokens() {
		if tok.GetUserId() == f.adminID {
			adminTokenID = tok.GetId()
		}
	}
	_, err = opTokens.DeleteApiToken(ctx, &adminv1.DeleteApiTokenRequest{Id: adminTokenID})
	wantCode(t, err, connect.CodeNotFound)

	// Changing your own password needs the current one.
	_, err = opUsers.SetUserPassword(ctx, &adminv1.SetUserPasswordRequest{Id: op.GetId(), Password: "new-password"})
	wantCode(t, err, connect.CodePermissionDenied)
	if _, err := opUsers.SetUserPassword(ctx, &adminv1.SetUserPasswordRequest{
		Id: op.GetId(), Password: "new-password", CurrentPassword: "operator-pass",
	}); err != nil {
		t.Fatal(err)
	}
	// Admins reset other users' passwords without it.
	if _, err := adminUsers.SetUserPassword(ctx, &adminv1.SetUserPasswordRequest{Id: op.GetId(), Password: "reset-password"}); err != nil {
		t.Fatal(err)
	}

	// Revoking a token stops it working.
	if _, err := opTokens.DeleteApiToken(ctx, &adminv1.DeleteApiTokenRequest{Id: tokRes.GetApiToken().GetId()}); err != nil {
		t.Fatal(err)
	}
	_, err = opUsers.GetUser(ctx, &adminv1.GetUserRequest{Id: op.GetId()})
	wantCode(t, err, connect.CodeUnauthenticated)

	// The last admin cannot be demoted or deleted.
	me, err := adminUsers.GetUser(ctx, &adminv1.GetUserRequest{Id: f.adminID})
	if err != nil {
		t.Fatal(err)
	}
	demoted := proto.CloneOf(me.GetUser())
	demoted.Admin = false
	_, err = adminUsers.UpdateUser(ctx, &adminv1.UpdateUserRequest{User: demoted})
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = adminUsers.DeleteUser(ctx, &adminv1.DeleteUserRequest{Id: f.adminID})
	wantCode(t, err, connect.CodeFailedPrecondition)

	// Deleting a user revokes their tokens.
	if _, err := adminUsers.DeleteUser(ctx, &adminv1.DeleteUserRequest{Id: op.GetId()}); err != nil {
		t.Fatal(err)
	}
	all, err = adminTokens.ListApiTokens(ctx, &adminv1.ListApiTokensRequest{})
	if err != nil || len(all.GetApiTokens()) != 1 {
		t.Fatalf("tokens after delete = %v, %v", all, err)
	}
}

func TestListAdapters(t *testing.T) {
	f := newAPIFixture(t)
	res, err := adminv1connect.NewAdapterServiceClient(f.client(f.adminToken)).ListAdapters(t.Context(), &adminv1.ListAdaptersRequest{})
	if err != nil || len(res.GetAdapters()) != 0 {
		t.Fatalf("adapters = %v, %v", res, err)
	}
}

// The served OpenAPI document is valid JSON for OpenAPI 3.1 and documents
// every REST route declared in the protos.
func TestOpenAPI(t *testing.T) {
	f := newAPIFixture(t)
	code, body, hdr := f.rest(t, http.MethodGet, "/openapi.json", "", "")
	if code != http.StatusOK || hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("openapi = %d %s", code, hdr.Get("Content-Type"))
	}
	var doc struct {
		OpenAPI string                    `json:"openapi"`
		Info    struct{ Title string }    `json:"info"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") || doc.Info.Title != "beenfar" {
		t.Fatalf("openapi %q, title %q", doc.OpenAPI, doc.Info.Title)
	}

	var routes []string
	protoregistry.GlobalFiles.RangeFilesByPackage("beenfar.config.v1", collectRoutes(&routes))
	protoregistry.GlobalFiles.RangeFilesByPackage("beenfar.admin.v1", collectRoutes(&routes))
	if len(routes) < 20 {
		t.Fatalf("only found %d annotated routes", len(routes))
	}
	for _, r := range routes {
		method, path, _ := strings.Cut(r, " ")
		// The generator writes nested path params in JSON form.
		path = strings.Replace(path, "{wifi_network.id}", "{wifiNetwork.id}", 1)
		if _, ok := doc.Paths[path][method]; !ok {
			t.Errorf("openapi lacks %s %s", method, path)
		}
	}
}

func collectRoutes(out *[]string) func(protoreflect.FileDescriptor) bool {
	return func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			methods := fd.Services().Get(i).Methods()
			for j := range methods.Len() {
				rule, _ := proto.GetExtension(methods.Get(j).Options(), annotations.E_Http).(*annotations.HttpRule)
				if rule == nil {
					continue
				}
				for method, path := range map[string]string{
					"get": rule.GetGet(), "post": rule.GetPost(), "put": rule.GetPut(),
					"delete": rule.GetDelete(), "patch": rule.GetPatch(),
				} {
					if path != "" {
						*out = append(*out, method+" "+path)
					}
				}
			}
		}
		return true
	}
}

// An RPC produces a server span that parents the store spans beneath it.
func TestRPCTracing(t *testing.T) {
	f := newAPIFixture(t)
	wifi := configv1connect.NewWifiNetworkServiceClient(f.client(f.adminToken))
	if _, err := wifi.CreateWifiNetwork(t.Context(), &configv1.CreateWifiNetworkRequest{WifiNetwork: wpaNetwork("traced")}); err != nil {
		t.Fatal(err)
	}

	spans := f.spans.Ended()
	byName := func(name string) sdktrace.ReadOnlySpan {
		for _, s := range slices.Backward(spans) {
			if s.Name() == name {
				return s
			}
		}
		t.Fatalf("no span %q in %d spans", name, len(spans))
		return nil
	}
	rpcSpan := byName("beenfar.config.v1.WifiNetworkService/CreateWifiNetwork")
	storeSpan := byName("store.CreateWifiNetwork")
	if storeSpan.Parent().SpanID() != rpcSpan.SpanContext().SpanID() {
		t.Errorf("store span parent = %v, want rpc span %v", storeSpan.Parent().SpanID(), rpcSpan.SpanContext().SpanID())
	}
	enduser := false
	for _, kv := range storeSpan.Attributes() {
		if kv.Key == "enduser.id" && kv.Value.AsString() == f.adminID {
			enduser = true
		}
	}
	if !enduser {
		t.Errorf("store span lacks enduser.id: %v", storeSpan.Attributes())
	}
}
