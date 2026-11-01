# phenix JSON Schemas

This directory holds one JSON Schema (draft 2020-12) per phenix config kind,
and `defs.schema.json`, which they share. Each per-kind schema describes a
whole config file, as `phenix config create` checks it. `workflow.schema.json`
describes the workflow config (`phenix.yml`) that `phenix workflow apply` reads:

| File | Kind | `apiVersion` |
|---|---|---|
| `topology.schema.json` | Topology | `phenix.sandia.gov/v1` |
| `scenario.schema.json` | Scenario | `phenix.sandia.gov/v2` |
| `experiment.schema.json` | Experiment | `phenix.sandia.gov/v1` |
| `image.schema.json` | Image | `phenix.sandia.gov/v1` |
| `role.schema.json` | Role | `phenix.sandia.gov/v1` |
| `user.schema.json` | User | `phenix.sandia.gov/v1` |
| `workflow.schema.json` | Workflow | `phenix.sandia.gov/v0` |

`phenix.schema.json` selects one of these schemas from the `apiVersion` and
`kind` of a file, not from its file name. It checks a file only when its
`apiVersion` starts with `phenix.sandia.gov/`. It accepts every other YAML
file, so you can apply it to all YAML files in a directory.

Each schema requires:

- `apiVersion`: the version phenix stores the kind at,
- `kind`: the config kind,
- `metadata`: the metadata every phenix config carries (`name` is required),
- `spec`: the kind's spec, whose schema is in `defs.schema.json`.

`workflow.schema.json` requires only `apiVersion`, `kind` and `spec`, and holds
its whole schema. phenix checks a workflow config against the `Workflow` schema
in `src/go/types/version/schemas/v0.yaml`. It expands `${VAR}` references in
the file first, so an editor checks the text before that expansion.

`defs.schema.json` holds the spec schema of each kind, and the schemas those
reference, once. The per-kind schemas reference it by a relative URI. A
schema loaded from a local file finds it in the same directory. A schema
loaded over HTTP finds it relative to its `$id`, which is on `main`. Thus a
copy of these files at a different URL still reads `defs.schema.json` from
`main`. Validators such as Ajv, check-jsonschema and python-jsonschema also
use `$id`, unless you give them all the files (for example Ajv `addSchema` of
each, or check-jsonschema `--base-uri file://<dir>/`).

phenix checks the spec of a config against the embedded OpenAPI schema file
of its `apiVersion` (`src/go/types/version/schemas/<version>.yaml`). Each
`spec` schema comes from the file of the kind's stored `apiVersion`: `v2.yaml`
for Scenario and `v1.yaml` for the other kinds. phenix refuses a config at an
`apiVersion` whose file does not describe its kind, such as a
`phenix.sandia.gov/v1` Scenario, and these schemas refuse it too. Checks phenix does not make are left out, so the
configs phenix itself writes validate: string formats it does not check (such
as `ipv4`, where it writes `""` for no address) are dropped, and a field it
writes as `null` (such as an interface's `dns`) accepts `null`.

The files are used for:

- editor validation and completion (VS Code, IntelliJ, and others),
- SchemaStore submissions,
- CI validation pipelines that need stable schema URLs.

## Use in an editor

phenix config files do not have standard names. Thus an editor cannot find
the schema of a file from its name alone. Use `phenix.schema.json` for all
YAML files in a repository or directory that holds phenix configs.

For the YAML extension for VS Code, or any editor that uses
`yaml-language-server`, add this to the `.vscode/settings.json` file of the
repository:

```json
{
  "yaml.schemas": {
    "https://raw.githubusercontent.com/sandialabs/sceptre-phenix/main/schemas/phenix.schema.json": [
      "**/*.yml",
      "**/*.yaml"
    ]
  }
}
```

To set the schema of one file, name it on the first line of the file:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/sandialabs/sceptre-phenix/main/schemas/phenix.schema.json
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: example
spec:
  nodes: []
```

You can also name a per-kind schema, such as `topology.schema.json`. A
per-kind schema refuses a file of a different kind.

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

`schemastore.catalog.fragment.json` is a ready-to-copy catalog entry for
`phenix.schema.json`, with the raw GitHub URL of the file on `main`. The other
files need no entry, because editors get them through the references in
`phenix.schema.json`.

SchemaStore selects a schema from the path of a file only, through the
`fileMatch` globs of the entry. SchemaStore does not accept generic globs,
such as `**/topology.yaml`, which also match files of other tools. Thus the
fragment matches only these phenix directories and file names:

- each YAML file under a `phenix-configs` directory, which `phenix workflow apply` reads,
- each YAML file in a `phenix-images` directory,
- the workflow config `phenix.yml`, and the legacy `.phenix.yml`.

`phenix.schema.json` then selects the schema from the content of the file.
For phenix configs in other locations, set the schema in the editor settings
or in the file, as shown in [Use in an editor](#use-in-an-editor).
