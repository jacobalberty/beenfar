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

## Todo
* Device adapters: UniFi provisioning and external adapters
* Terraform provider
* Per device command queues
* Translate internal configuration data to per device data
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
