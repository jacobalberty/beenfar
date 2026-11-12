[![Coverage Status](https://coveralls.io/repos/github/jacobalberty/beenfar/badge.svg)](https://coveralls.io/github/jacobalberty/beenfar)
[![CodeQL](https://github.com/jacobalberty/beenfar/workflows/CodeQL/badge.svg)](https://github.com/jacobalberty/beenfar/actions?query=workflow%3ACodeQL)

# Has Anyone Really Been Far Even as Decided to Use Even Go Want to do Look More Like?

## What is this?

This is a work in progress network controller to support centralized provisioning and configuration of different types of network gear.

This is still very early on and so far only contains the backend code.

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

**RADIUS servers.** WPA-enterprise wifi networks name a RADIUS server
(`radius_server_id`; the old free-text `radius_profile` is deprecated). The
server's shared secret is write-only: no response contains it, and an update
without one keeps it. `shared_secret_version` increases whenever it changes.

**Switch ports.** Ports of adopted devices can be given settings (name,
disabled, native network, tagged networks, PoE mode) by device id and
1-based port index. A port without settings runs with the device's
defaults; deleting them restores the defaults.

## API module

The protobuf API and its generated Go clients, together with the adapter
SDK, are the module `github.com/jacobalberty/beenfar/api` in `api/`, which
the Terraform provider and external adapters import. It is released on its
own, as `api/vX.Y.Z` tags: every push to `main` that touches `api/` runs CI
and tags a release when the commits that touched `api/` since the last one
call for it (`feat` minor, `fix`/`perf` patch, breaking changes minor while
0.x; see `.github/scripts/api-next-version.sh`). Commits that touch `api/`
must therefore follow [Conventional Commits](https://www.conventionalcommits.org/),
which a pull request check enforces.

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
| `BEENFAR_UNIFI_SYSTEM_CFG` | `false` | Send adopted devices their wifi, VLAN and RADIUS settings (experimental, see below) |
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

**Device settings (experimental).** With `BEENFAR_UNIFI_SYSTEM_CFG=true`,
adopted devices are sent their wifi networks (WPA-PSK, and WPA-enterprise
with a RADIUS server for authentication and optional accounting), the VLANs
those networks use, and the matching bridges, as a `system_cfg` document
(`renderSystemConfig` in `adapters/unifi/systemcfg.go`). The key names come
from a `system.cfg` captured from a dual-radio access point with the madwifi
driver, and the tests check every rendered key against that capture. What is
not verified, and why it is off by default:

- the document only holds those sections, not radio channels, SSH users,
  NTP and the other settings beenfar does not manage, and whether a device
  merges it into its configuration or replaces its configuration with it has
  not been tried on hardware;
- other radio layouts (newer models use other interface names);
- open, WEP and guest networks are not rendered (a config containing one is
  refused, and the push fails with an error), nor is more than one RADIUS
  server per network;
- switch port settings are stored and handed to the adapter, but not sent to
  devices: the capture has no port VLAN or PoE keys.

Without it, adopted devices are re-keyed and tracked by config revision, but
their settings are left alone.

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
