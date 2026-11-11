// Package openapi embeds the generated OpenAPI 3.1 description of beenfar's
// REST API.
package openapi

import _ "embed"

// JSON is the OpenAPI document, generated from the protos by buf.
//
//go:embed beenfar.openapi.json
var JSON []byte
