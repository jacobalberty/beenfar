[![Coverage Status](https://coveralls.io/repos/github/jacobalberty/beenfar/badge.svg)](https://coveralls.io/github/jacobalberty/beenfar)
[![CodeQL](https://github.com/jacobalberty/beenfar/workflows/CodeQL/badge.svg)](https://github.com/jacobalberty/beenfar/actions?query=workflow%3ACodeQL)

# Has Anyone Really Been Far Even as Decided to Use Even Go Want to do Look More Like?

## What is this?

This is a work in progress network controller to support centralized provisioning and configuration of different types of network gear.

This is still very early on and so far only contains the backend code.

> [!WARNING]
> **Pre-release testing software.** While the version is `v0.x.x`, beenfar is
> pre-release software intended for testing and is expected to break regularly.
>
> The `1.0.0` release will ship with a simpler schema for the initial release.
> If you choose to use beenfar before then, expect to back up your data and
> restore it on the latest `0.x.x` release before migrating to `1.0.0`.

## Running

```sh
BEENFAR_ADMIN_PASSWORD='change me please' go run ./cmd/beenfard -db sqlite://beenfar.db
```

On first start, with no users in the database, `BEENFAR_ADMIN_PASSWORD` creates
an admin (named by `BEENFAR_ADMIN_USER`, default `admin`). Without it, create
one offline with `beenfard admin create`, which reads the password from
standard input.

For Terraform and scripts, issue an API token, either offline:

```sh
beenfard admin token -username admin -name terraform
```

or through the API (`ApiTokenService.CreateApiToken`). Send it as
`Authorization: Bearer <token>`.

### With Docker, and the web UI

`compose.yaml` runs beenfard, the [web UI](https://github.com/jacobalberty/LookMoreLike)
and a small proxy that puts them on one origin, with host networking so UniFi
devices inform the machine's own address:

```sh
cp .env.example .env   # set BEENFAR_ADMIN_PASSWORD
docker compose up -d   # then open http://<this host>:8000
```

[deploy/README.md](deploy/README.md) has the details, and how to run it on its own
LAN address (ipvlan), behind Traefik, or behind Pangolin.

## Configuration API

Everything that configures the network is a resource in `beenfar.config.v1`,
served over Connect, gRPC and REST (`/api/v1/...`, described at
`/openapi.json`): networks, wifi networks, RADIUS servers, switch ports and
devices. The UI and the [Terraform provider](https://github.com/jacobalberty/terraform-provider-beenfar)
use the same RPCs. Resources have server-assigned ids and an `etag`; send the
etag back on update or delete to fail with `ABORTED` if someone changed the
resource in the meantime.

**Partial updates.** Every `Update` takes an optional `update_mask`
([AIP-134](https://google.aip.dev/134)) naming the fields to change; the
others keep their stored values. An empty mask (or `*`) replaces every
field. Over REST, use `PATCH` with `?update_mask=ssid,securityKey`. This is
what lets Terraform manage some fields of a resource while the UI manages
others: Terraform sends only the fields in its configuration, so for
instance the access point groups of a wifi network can be left to the UI.

**Who set what.** Networks, wifi networks, RADIUS servers and switch ports
report `field_writers`: for each field that has been written, the user or API
token (by name, e.g. `terraform-ci`) that last set it, and when. A create
records every field; an update records the fields named in its `update_mask`
(all of them for an empty mask). This is a record of writes, not of
ownership: beenfar cannot know that a client "manages" a field, only that it
named it, and fields written before this was recorded have no entry. It is not
part of the `etag`.

**RADIUS servers.** WPA-enterprise wifi networks name a RADIUS server
(`radius_server_id`; the old free-text `radius_profile` is deprecated). The
server's shared secret is write-only: no response contains it, and an update
without one keeps it. `shared_secret_version` increases whenever it changes.

**Switch ports.** Ports of adopted devices can be given settings (name,
disabled, native network, tagged networks, PoE mode) by device id and
1-based port index. A port without settings runs with the device's
defaults; deleting them restores the defaults. Only devices with ports of their
own take settings: a device reports `kind` and `port_count`, and most access
points report none, so a UI should offer port settings only when `port_count`
is above zero (the API refuses ports on a device that has none, or beyond its
count; a device whose adapter did not say what it is is not restricted).

**Access points.** An adopted access point is provisioned automatically: it
is sent every wifi network (with its VLAN and RADIUS settings) as soon as it is
adopted and speaking its own key (the replies that hand it that key are sealed
with the public default key, so they carry no secrets), and again whenever
they change. Adoption itself is still a decision:
`AdoptDevice`. To change what one access point runs, set its `ap_group` with
`UpdateDevice`: it then runs the networks that are not limited to groups plus
those that list its group, and no longer the networks limited to other groups.
A network can list several groups (`ap_groups`). Clearing `ap_group` returns
the access point to running everything.

**TypeScript client.** The same release also publishes the generated
TypeScript client (messages and service descriptors, for
`@connectrpc/connect-web`) as the npm package `@jacobalberty/beenfar-api` to
the [GitHub Packages npm registry](https://docs.github.com/packages/working-with-a-github-packages-registry/working-with-the-npm-registry),
at the same version as the Go module. GitHub's registry asks for a token with
`read:packages` even for public packages; see `api/ts/README.md` for the
`.npmrc` and an example. The sources are generated into `api/gen/ts` by
`go generate` in `api/` (which runs `npm clean-install` for the pinned
`protoc-gen-es`, so it needs Node) and CI checks them like the Go output.

## Web UI

beenfard serves only the API; the web UI is a separate project and container,
[LookMoreLike](https://github.com/jacobalberty/LookMoreLike), which talks to the
same RPCs as Terraform. Browsers authenticate with the session cookie, and
beenfar refuses cookie, Basic and proxy-authenticated requests whose `Origin`
is not the request's `Host`. The UI and the API must therefore be served from
**one origin**, by a reverse proxy that **keeps the `Host` header** (Traefik
and Caddy do by default; set `X-Forwarded-Proto` when it terminates TLS).
Route these paths to beenfard and everything else to the UI:

| Path | Why |
|---|---|
| `/beenfar.` prefix (e.g. `/beenfar.config.v1.WifiNetworkService/...`) | Connect, gRPC and gRPC-Web, which the UI uses |
| `/api/` | the REST binding of the same RPCs |
| `/auth/` | OpenID Connect login and callback (browser navigations) |
| `/openapi.json`, `/healthz` | the API description and health check |
| `/inform` | UniFi devices (usually reached directly on beenfard's port, not through the UI's hostname) |

A cross-origin API URL is not supported: it would need CORS and cookies
that cross sites, which beenfar does not offer. [deploy/README.md](deploy/README.md)
has ready-made compose files for this.

## Authentication

API tokens (`Authorization: Bearer`) and the password login form are always
available; password login can be turned off. Everything else is off until
configured. Each setting is an environment variable with a matching flag
(`beenfard -h`).

| Variable | Default | Meaning |
|---|---|---|
| `BEENFAR_PUBLIC_URL` | from request | External base URL, used for OIDC redirects |
| `BEENFAR_AUTH_PASSWORD_LOGIN` | `true` | Offer the username/password form |
| `BEENFAR_AUTH_MAX_FAILURES_PER_USER` / `_PER_IP` | `10` / `50` | Failed password sign-ins allowed per window |
| `BEENFAR_AUTH_FAILURE_WINDOW` | `15m` | Window for those limits |
| `BEENFAR_AUTH_BASIC` | `false` | Accept HTTP Basic with an API token as the password |
| `BEENFAR_AUTH_BASIC_PASSWORDS` | `false` | Also accept real passwords over Basic (implies `BASIC`) |
| `BEENFAR_AUTH_BASIC_CACHE_TTL` | `5m` | How long a verified Basic password is cached |

### Trusted reverse proxy

Any proxy that authenticates users and passes their name in a header works:
name the header, and the addresses the proxy connects from. Headers from any
other address are ignored, so the proxy must be the only way in or the
trusted range must be tight.

| Variable | Meaning |
|---|---|
| `BEENFAR_AUTH_PROXY_TRUSTED_CIDRS` | Comma-separated peer CIDRs allowed to assert identity (required) |
| `BEENFAR_AUTH_PROXY_USER_HEADER` | Header with the username (required) |
| `BEENFAR_AUTH_PROXY_GROUPS_HEADER` / `_GROUPS_SEPARATOR` | Optional groups header and separator (`,`) |
| `BEENFAR_AUTH_PROXY_ADMIN_GROUPS` | Groups whose members are admins; when set, admin follows the header |
| `BEENFAR_AUTH_PROXY_LOGOUT_URL` | Where the UI sends users to sign out |

| Proxy | User header | Groups header |
|---|---|---|
| oauth2-proxy (`--set-xauthrequest`/`--pass-user-headers`) | `X-Forwarded-User` | `X-Forwarded-Groups` |
| Authelia, Authentik (forward auth) | `Remote-User` | `Remote-Groups` |
| Pomerium | `X-Pomerium-Claim-Email` | `X-Pomerium-Claim-Groups` |

### OpenID Connect

List provider ids in `BEENFAR_AUTH_OIDC_PROVIDERS` (e.g. `corp,home-lab`)
and configure each with `BEENFAR_AUTH_OIDC_<ID>_*`, where `<ID>` is the id
upper-cased with other characters replaced by `_`:

| Suffix | Meaning |
|---|---|
| `ISSUER`, `CLIENT_ID` | Required |
| `CLIENT_SECRET` or `CLIENT_SECRET_FILE` | Client secret (file for Docker secrets) |
| `DISPLAY_NAME` | Button label (default: the id) |
| `SCOPES` | Default `openid profile email` |
| `USERNAME_CLAIM` / `GROUPS_CLAIM` | Default `preferred_username` / `groups` |
| `ADMIN_GROUPS` | Groups whose members are admins; when set, admin follows the claim at each sign-in |

Register `<public URL>/auth/oidc/<id>/callback` as the redirect URI with the
provider. External users are created on first sign-in, have no password, and
are matched by the provider's subject, so renames upstream keep the account.
A local account with the same name is never taken over.


The API is defined in protobuf under [`api/proto`](api/proto) and is the same
for every client, whether Terraform, the UI or scripts:

* `beenfar.config.v1`: wireless networks, networks (VLANs/subnets) and device
  adoption. These are the resources Terraform manages.
* `beenfar.admin.v1`: users, API tokens, browser login and adapter status.

Each RPC is served over Connect, gRPC and gRPC-Web at its service path, and as
REST under `/api/v1/...`:

```sh
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/wifi-networks
```

The OpenAPI 3.1 description of the REST API is served at `/openapi.json`.
Go clients are generated into the separate `github.com/jacobalberty/beenfar/api`
module.

Generated code lives only under `gen/` directories. Type conversions between
the API, domain and database types are generated with
[goverter](https://github.com/jmattheis/goverter) from the `Converter`
interfaces in `internal/apiconv` and `internal/store/sqlite/dbconv`.

To regenerate code after editing the protos, SQL queries or converters:

```sh
(cd api && go generate -run install . && go generate .)   # buf, Go + Connect, OpenAPI
go generate ./...                                       # sqlc, goverter
```

## Device adapters

Adapters speak a vendor's device protocol. The UniFi adapter is built in;
others run as separate processes and connect to the controller.

### UniFi

Point devices at `http://<controller>:8080/inform` (DNS or `set-inform`). An
unknown device is recorded as pending (and answered 404, so it keeps
trying); adopt it through the API (`AdoptDevice`) or Terraform. Adoption gives
the device a private key, which is stored in the database and kept across
restarts; a device that loses the reply keeps working, because its old key is
accepted until it has switched.

| Variable | Default | Meaning |
|---|---|---|
| `BEENFAR_UNIFI_INFORM_INTERVAL` | `10` | Seconds between informs of a device |
| `BEENFAR_UNIFI_GCM` | `false` | Switch devices to AES-GCM when adopting them |
| `BEENFAR_UNIFI_READOPT_AFTER` | `2m` | How long an adopted device must be silent before one on the default key is taken for factory reset and offered for re-adoption; `0` turns it off |

**Reset devices.** An adopted device that comes back speaking the default key
was factory reset. It stays the same adopted device and the API reports it
with `readoption: AWAITING_ACCEPTANCE`, so a UI can show "needs re-adoption"
rather than a new device; it is not listed among pending devices. Nothing is
sent to it until you accept, with the same `AdoptDevice` call used for new
devices (`readoption` is then `IN_PROGRESS` until the device runs on its new
key). It is never re-keyed automatically: the default key is public, so the
reply would be readable by anyone on the network, and anyone can send an
inform with it for any MAC. Only a device that has been silent for
`BEENFAR_UNIFI_READOPT_AFTER` is considered, so a live device is not flagged by
a spoofed inform. With that set to `0` a reset device stays locked, and has to
be forgotten and adopted again.

**Forgetting a device.** `ForgetDevice` factory resets an adopted device, so it
comes back as a new, pending device that can be adopted again. The reset is a
message the controller sends in reply to the device's next inform (the one a UniFi
controller sends when you forget a device), so the device stays listed with
`forgetting: true` until it has reset, and is removed once it informs on the
default key. A device that is switched off stays in that state; `force` (REST:
`DELETE /api/v1/devices/{id}?force=true`) gives up waiting and removes it now,
but the device then still holds the key beenfar discarded, so it shows as locked
until it is reset by hand (or its key is imported again). A pending or locked
device has nothing to reset and is removed at once. Only one captured controller
reply backs the message, so which models and firmware honour it is not
verified; see [go-unifi](https://github.com/jacobalberty/go-unifi).

**Device settings.** Adopted access points are sent their wifi networks
(WPA-PSK, and WPA-enterprise with a RADIUS server for authentication and
optional accounting, filtered by the access point's `ap_group`), the VLANs those
networks use, and the matching bridges, as a `system_cfg` document
(`renderSystemConfig` in `adapters/unifi/systemcfg.go`). The key names come
from a `system.cfg` captured from a dual-radio access point with the madwifi
driver, and the tests check every rendered key against that capture. What is
not verified:

- the document only holds those sections, not radio channels, SSH users,
  NTP and the other settings beenfar does not manage, and whether a device
  merges it into its configuration or replaces its configuration with it has
  not been tried on hardware;
- other radio layouts (newer models use other interface names);
- open, WEP and guest networks are not rendered, nor is more than one RADIUS
  server per network. A configuration containing one cannot be rendered: the
  access point is still adopted and keeps tracking the config revision, but is
  sent no settings, and the daemon logs a warning on each inform;
- an access point left with no networks to run (every network was deleted, or
  it was moved to a group no network lists) is sent no settings: the capture does
  not show how to remove a WLAN, so the networks it already runs keep running
  until the device is reset or re-adopted;
- switch port settings are stored and handed to the adapter, but not sent to
  devices: the capture has no port VLAN or PoE keys.

Switches and gateways are re-keyed and tracked by config revision, but their
settings are left alone.

**Taking over devices from another controller.** Extract each device's
credentials with an outside tool (the `x_authkey` in the old controller's
MongoDB, or `mgmt.authkey` in the device's `/var/etc/persisted/cfg/mgmt`),
write them as `[{"mac": "...", "authkey": "32 hex digits", "gcm": false}]` and
run `beenfard admin import-unifi -file keys.json` (or call
`AdapterService.ImportUnifiDeviceKeys` as an admin). Once the device is
pointed at beenfar it appears as pending with its real model; adopting it
re-keys it. Keys are never returned by any API. A device that no key opens is
listed with `locked: true`. Setting a device's inform URL over SSH is left to
outside tools.

### External adapters

An external adapter uses the SDK in `github.com/jacobalberty/beenfar/api/adapter`:
implement `adapter.Adapter` and call `adapter.RunRemote(ctx, url, token, impl)`.
It registers with the controller, receives adopt and forget commands over a
stream, and reports devices and reads configuration through a Connect-backed
`Host`. Traces started at the API continue inside the adapter. Create a token
for it with `beenfard admin adapter-token create -adapter NAME` (also `list`
and `revoke`); the token lets its holder act as that adapter only.

## End-to-end tests

`e2e/` runs the real `beenfard` binary as a subprocess against a fleet of
emulated UniFi devices ([unifi-emu](https://github.com/jamesbraid/unifi-emu)),
and drives it through the public API, the way an operator or the Terraform
provider would. The devices speak the real inform protocol to `/inform`, so
adoption, the key handshake, AES-GCM and forgetting a device are exercised
exactly as hardware would. It needs no containers and no privileges.

```sh
go test -tags e2e -count=1 -v ./e2e/...
```

The suite is behind the `e2e` build tag, so `go test ./...` (and the
Dockerfile's build stage) never run it; CI has an `end-to-end` job. It builds
`beenfard` from the checkout; set `BEENFAR_E2E_BIN` to test a prebuilt binary
(for example one copied out of the release image). A failing test prints the
daemon's log.

### How a test is built

Each test starts its own daemon (random loopback port, fresh SQLite database,
an admin and an API token, UniFi inform interval of one second) and its own
fleet, so tests do not share state:

```go
func TestSomething(t *testing.T) {
	d := startDaemon(t)                       // add settings: startDaemon(t, "BEENFAR_UNIFI_GCM=true")
	f := startFleet(t, d, accessPoint)        // devices inform d once a second

	pending := d.pending(t, accessPoint.MAC)  // wait until the daemon lists it as pending
	d.adopt(t, pending[accessPoint.MAC])      // AdoptDevice through the API
	connected(t, d, f, accessPoint.MAC)       // device finished the handshake and the daemon says adopted

	// ...then call whatever you are testing and assert on the response.
	wifi := configv1connect.NewWifiNetworkServiceClient(d.client)
	_ = wifi
}
```

The helpers are at the top of `e2e/e2e_test.go`:

| Helper | Does |
|---|---|
| `startDaemon(t, env...)` | Runs `beenfard`; `env` entries (`KEY=value`) are added to the baseline and win over it. Returns a `daemon` with the base `url`, an authenticated Connect `client` and a ready `devices` client. |
| `startFleet(t, d, specs...)` | Starts emulated devices informing `d`. Returns a `fleet`, which is the emulator's `*emu.Emu`: `State`, `WaitState`, `Stop`. |
| `d.pending`, `d.adopt`, `d.state`, `connected`, `lastSeen` | Wait for and drive device adoption. |
| `fleet.tap` | Records the encoding flags of every inform the devices send, by MAC (`count`, `waitInforms`). Use it to assert how devices talk to the daemon, e.g. that they use AES-GCM, rather than only that they keep being answered. |
| `eventually(t, timeout, what, check)` | Polls `check(ctx)` until it returns nil. Use it for anything asynchronous; do not `time.Sleep`. `ctx` ends at the deadline: pass it to every request `check` makes, so a stalled response cannot outlive the wait. Outside `eventually`, give an API call `callCtx(t)`. |

### Extending them

- **A new scenario**: add a `TestXxx` function in `e2e/` shaped like the one
  above. Assert on API responses, and on what the device sees through
  `fleet.State`. Keep every wait bounded with `eventually` or a context.
- **A new API under test**: build its client from `d.client`. Networks, wifi
  networks and RADIUS servers can be created through the API with no devices
  at all, so those tests need only `startDaemon`; switch ports need an adopted
  switch.
- **A new daemon setting**: pass it to `startDaemon` as an environment
  variable, as `TestAdoptionWithAESGCM` does. Settings are `BEENFAR_*`
  variables, the same as in production (`beenfard -h` lists them).
- **A different device**: add an `emu.DeviceSpec`. `Model` must be a model the
  emulator knows (access points, switches, gateways; see
  `model_profiles.json` in the unifi-emu module). Give each device a fixed,
  distinct MAC so a failure can be reproduced. `Ports`, `Name` and `SSIDs`
  shape what it reports.
- **Different adoption behaviour**: if a setting makes some devices adopt on
  their own, have the test adopt only the devices that are still pending.

### Limits

- The emulator understands `noop`, `setparam` (the `authkey`, `cfgversion` and
  `use_aes_gcm` of `mgmt_cfg`), `setstate`, `upgrade`, `reboot` and the
  `setdefault` factory reset. It does not interpret `system_cfg`, so these
  tests cannot show that the wifi, VLAN and RADIUS settings pushed to access
  points are correct; that is checked against a real device's key names in
  `adapters/unifi`. They do show that an access point accepts them and keeps
  informing.
- The emulator logs through the global standard logger, so tests that start a
  fleet must not use `t.Parallel`. The suite is a few seconds long, so that
  costs little.
- A daemon behaviour that depends on a reply a real device has not been seen
  to accept is only as good as the emulator's model of it. Say so in the test
  comment, as the go-unifi `SetDefault` documentation does.
- If you need a change in the emulator (a message it ignores), fix it in
  unifi-emu and bump the dependency; do not work around it in the test. Skip
  the test with a comment naming the missing support until it is released.

## Todo
* Per device command queues
* Verify the UniFi `system_cfg` push on hardware, and render guest networks and switch ports
* [Frontend](https://github.com/jacobalberty/LookMoreLike)

## Supported device types

### In process
* UniFi access points
* UniFi network switches

### Planned
* OpenWRT
* EdgeOS devices
* Mobile device provisioning
* UniFi gateways

## Data storage
The database layer will be a special device type that accepts all data types and automatically provides its data to the data layer on startup.

Data will be persisted to the dastabase as part of the standard provisioning process, the data storage will just be another device that gets provisioned when data changes.
