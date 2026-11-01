package types

import _ "embed" // embeds the config schema

// OpenAPI is the OpenAPI document whose Config schema every config must
// match: its apiVersion, kind, metadata and spec. Each kind's spec has its own
// schema, in the version package's schema files.
//
//go:embed schemas/config.yaml
var OpenAPI []byte
