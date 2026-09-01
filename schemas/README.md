# phenix JSON Schemas

This directory contains static, committed JSON Schema files exported from the embedded phenix OpenAPI component schemas.

These files are intended for:

- SchemaStore submissions.
- Editor validation and completion (VS Code, IntelliJ, etc.).
- CI validation pipelines that need stable schema URLs.

## Generate or Refresh Schemas

From the repository root:

```bash
make generate
```

Or run only schema export:

```bash
cd src/go
make generate-schemas
```

The exporter command is:

```bash
go run ./tools/schema-export -out ../../schemas
```

## Files

Schemas are grouped by API/OpenAPI version:

- `schemas/v1/*.schema.json`
- `schemas/v2/*.schema.json`

Each file is a standalone JSON Schema document with:

- `$schema` set to JSON Schema draft 2020-12.
- `$ref` pointing at the requested top-level kind.
- `$defs` containing all component schemas needed for cross-schema references.

## SchemaStore Submission Notes

When submitting to SchemaStore:

1. Add one catalog entry per schema file you want discoverable.
2. Use raw GitHub URLs pointing to files in this directory.
3. Add `fileMatch` patterns for common phenix config file names.

Suggested starting set:

- `schemas/v1/topology.schema.json`
- `schemas/v1/scenario.schema.json`
- `schemas/v1/experiment.schema.json`
- `schemas/v1/image.schema.json`
- `schemas/v2/topology.schema.json`
- `schemas/v2/scenario.schema.json`
- `schemas/v2/experiment.schema.json`
- `schemas/v2/image.schema.json`

Example SchemaStore catalog entry:

```json
{
  "name": "phenix Topology (v2)",
  "description": "phenix Topology configuration schema (v2)",
  "fileMatch": ["topology.yml", "topology.yaml", "*topology*.yml", "*topology*.yaml"],
  "url": "https://raw.githubusercontent.com/sandialabs/sceptre-phenix/main/schemas/v2/topology.schema.json"
}
```

A ready-to-copy starter list is also provided in `schemas/schemastore.catalog.fragment.json`.
