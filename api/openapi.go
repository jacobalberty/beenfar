package api

import _ "embed"

// OpenAPIJSON is the OpenAPI 3.1 description of the REST API, generated from
// the protos by buf into gen/openapi.
//
//go:embed gen/openapi/beenfar.openapi.json
var OpenAPIJSON []byte
