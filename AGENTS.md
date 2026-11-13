# AGENTS.md

Guidance for AI coding agents working in this repository. Humans: see
[README.md](README.md), which this file refers to rather than repeats.

## What this is

beenfar is a network controller. The daemon `beenfard` (`cmd/beenfard`) serves a
configuration API (Connect, gRPC and REST, protobuf in `api/`) and the
device-facing `/inform` endpoint of the built-in UniFi adapter. A web UI and a
Terraform provider live in separate repositories and use the same API.

## Layout

| Path | What |
|---|---|
| `cmd/beenfard` | The daemon, and its `admin` subcommands |
| `api/` | Its own Go module: protobuf API, generated clients (`api/gen`), TypeScript client, adapter SDK (`api/adapter`). Released as `api/vX.Y.Z` |
| `adapters/unifi` | The built-in UniFi adapter (inform protocol, adoption, `system_cfg` rendering) |
| `internal/core` | Business logic between the API and the store |
| `internal/server` | HTTP, Connect and REST handlers, auth interceptors |
| `internal/adapterhost`, `internal/auth`, `internal/store`, `internal/telemetry` | Adapter hosting, authentication, SQLite persistence and queries, OpenTelemetry |
| `e2e/` | End-to-end tests (see below) |
| `deploy/`, `compose*.yaml` | Docker Compose stacks and the proxy |

## Commands

```sh
go build ./... && go vet ./...
go test -race -shuffle=on -count=1 ./...          # unit and in-process tests
go test -tags e2e -count=1 -v ./e2e/...           # end-to-end tests
(cd api && go generate -run install . && go generate .)   # regenerate API code (needs buf, Node)
go generate ./...                                  # sqlc, goverter
```

Lint with the version CI pins (`GOLANGCI_LINT_VERSION` in
`.github/workflows/go.yml`); a locally installed golangci-lint may be built with
an older Go than `go.mod` needs, so run it through the module system:

```sh
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...
```

## Conventions

- **Commits are Conventional Commits** (`type(scope): description`). Commits that
  touch `api/` must be, because `api/` releases are computed from them; a pull
  request check enforces it. Do not amend, fixup or force-push commits on an open
  pull request: fix review and CI findings in new commits.
- **Generated code lives only under `gen/` directories**, and CI fails when it is
  stale. Never edit it by hand; change the `.proto`, SQL query or converter
  and regenerate.
- **Logging** uses `log/slog` with a context on every call and attributes only
  (`sloglint` enforces it).
- **Dependencies**: add one only when neither the standard library nor an existing
  dependency covers the need.
- **Never commit secrets.** Test data derived from real devices keeps key names
  only (see `adapters/unifi/testdata/README.md`).
- Other agents may be working in the same checkout. Make changes in a git
  worktree (`git worktree add -b <branch> ../beenfar-<topic> origin/main`),
  not by switching branches in a shared checkout.

## End-to-end tests

`e2e/` starts the real `beenfard` binary and a fleet of emulated UniFi devices
([unifi-emu](https://github.com/jamesbraid/unifi-emu)) and tests through the
public API. [README.md](README.md#end-to-end-tests) explains how a test is
built, the helpers, and the emulator's limits. What agents must do:

- **Add or update an e2e test when a change is visible to a device or to an API
  client end to end**: adoption, forgetting or re-adopting devices, the inform
  exchange, daemon settings that change what devices are told, or an API flow
  across several RPCs. Pure logic, validation and storage stay in unit tests
  next to the code; e2e tests are for what only a running daemon with real
  devices shows.
- **Run `go test -tags e2e -count=1 ./e2e/...` before pushing** any change under
  `adapters/unifi`, `internal/adapterhost`, `internal/core`, `internal/server`,
  `internal/store`, `cmd/beenfard` or `api/`. It takes under half a minute and
  needs no containers.
- **Extend `e2e/e2e_test.go` rather than starting a parallel harness.** Start a
  daemon with `startDaemon(t, "BEENFAR_X=y")`, devices with `startFleet`, and
  wait with `eventually`; never `time.Sleep`. Use fixed MACs. Do not use
  `t.Parallel` in tests that start a fleet (the emulator logs globally).
- **A failing e2e test is a finding.** Do not loosen the assertion, lengthen a
  timeout or skip the test to get green. Find whether the daemon, the
  emulator or the test is wrong. If it is the emulator, fix it upstream, bump the
  dependency, and until then skip the single test with a comment naming what is
  missing.
- **Tests that build the daemon use `go build`, so a replace or `-modfile` is not
  seen by it unless set through `GOFLAGS`.** To try an unreleased dependency,
  run `GOFLAGS=-modfile=/path/alt.mod go test -tags e2e ./e2e/...`.
- The suite never talks to a real controller or real devices, and must not.
