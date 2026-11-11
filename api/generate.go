// Package api holds beenfar's protobuf definitions. Generated code lives in
// gen/go (Go messages and Connect clients/handlers) and gen/openapi (the
// OpenAPI 3.1 description of the REST binding).
package api

// Pinned code generators. `go generate -run install ./api` installs them.
//go:generate -command install go install
//go:generate install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
//go:generate install connectrpc.com/connect/v2/cmd/protoc-gen-connect-go@v2.0.0
//go:generate install connectrpc.com/protoc-gen-connect-openapi@v0.30.0

// gen/ holds only generated output, so it is wiped first; a hand-written
// file placed there would be deleted and fail the CI freshness check.
//go:generate rm -rf gen
//go:generate buf generate
