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

## API

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
