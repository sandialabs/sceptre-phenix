# phenix JSON Schemas

This directory holds one JSON Schema (draft 2020-12) per phenix config kind,
and `defs.schema.json`, which they share. Each per-kind schema describes a
whole config file, as `phenix config create` checks it:

| File | Kind | `apiVersion` |
|---|---|---|
| `topology.schema.json` | Topology | `phenix.sandia.gov/v1` |
| `scenario.schema.json` | Scenario | `phenix.sandia.gov/v2` |
| `experiment.schema.json` | Experiment | `phenix.sandia.gov/v1` |
| `image.schema.json` | Image | `phenix.sandia.gov/v1` |
| `role.schema.json` | Role | `phenix.sandia.gov/v1` |
| `user.schema.json` | User | `phenix.sandia.gov/v1` |

Each schema requires:

- `apiVersion`: the version phenix stores the kind at,
- `kind`: the config kind,
- `metadata`: the metadata every phenix config carries (`name` is required),
- `spec`: the kind's spec, whose schema is in `defs.schema.json`.

`defs.schema.json` holds the spec schema of each kind, and the schemas those
reference, once. Editors resolve it next to wherever they loaded a per-kind
file. Validators that honor `$id`, such as Ajv, check-jsonschema and
python-jsonschema, fetch it from `main` unless given both files (for example
Ajv `addSchema` of each, or check-jsonschema `--base-uri file://<dir>/`).

phenix checks the spec of every config against the latest embedded OpenAPI
schema file (`src/go/types/version/schemas/v2.yaml`), whatever the config's
`apiVersion`, so each `spec` schema comes from that file. Configs at an older
`apiVersion`, such as `phenix.sandia.gov/v1` Scenarios, are refused: phenix
refuses to create them too. Checks phenix does not make are left out, so the
configs phenix itself writes validate: string formats it does not check (such
as `ipv4`, where it writes `""` for no address) are dropped, and a field it
writes as `null` (such as an interface's `dns`) accepts `null`.

The files are used for:

- editor validation and completion (VS Code, IntelliJ, and others),
- SchemaStore submissions,
- CI validation pipelines that need stable schema URLs.

## Use in an editor

With the YAML extension for VS Code, or any editor using `yaml-language-server`,
name the schema on the first line of a config file:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/sandialabs/sceptre-phenix/main/schemas/topology.schema.json
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: example
spec:
  nodes: []
```

## Generate or Refresh Schemas

The files are generated from the embedded OpenAPI schemas; do not edit them.
From the repository root, code generation refreshes them:

```bash
make generate
```

Or run only the schema export:

```bash
make -C src/go generate-schemas
```

which runs:

```bash
cd src/go
go run ./tools/schema-export -out ../../schemas
```

Commit the regenerated files with the change to the OpenAPI schemas. CI runs
`make generate` and fails when the result differs from the committed files,
and `go test ./tools/schema-export` fails locally the same way. The Vitest
suite (`npm test` in `src/js`) compiles the files strictly, as SchemaStore
does, and validates configs against them.

## SchemaStore Submission Notes

`schemastore.catalog.fragment.json` is a ready-to-copy set of catalog entries,
one per config kind, using the raw GitHub URLs of the files in this directory;
`defs.schema.json` needs no entry, since editors fetch it through the per-kind
schemas' references. The fragment's `fileMatch` patterns match only the layout
phenix config repositories use,
such as [sceptre-phenix-topologies](https://github.com/sandialabs/sceptre-phenix-topologies):
`topology.yml`, `scenario.yml` and `experiment.yml` under a `phenix-configs`
directory, and image configs in a `phenix-images` directory. Role and User
configs have no conventional file name, so their entries have no `fileMatch`;
name their schema in the file as shown above.

Example SchemaStore catalog entry:

```json
{
  "name": "phenix Topology",
  "description": "phenix Topology config (apiVersion phenix.sandia.gov/v1)",
  "fileMatch": ["**/phenix-configs/**/topology.yml", "**/phenix-configs/**/topology.yaml"],
  "url": "https://raw.githubusercontent.com/sandialabs/sceptre-phenix/main/schemas/topology.schema.json"
}
```
