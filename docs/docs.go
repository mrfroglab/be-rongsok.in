// Package docs: spesifikasi OpenAPI (di-embed, disajikan di /api-docs).
package docs

import _ "embed"

//go:embed swagger.yaml
var Swagger []byte
