# Configuration File Schemas

The following schemas are all represented using the `OpenAPIv3` format. There
is one schema file for each config `apiVersion`, and it holds the schemas of the
kinds stored at that version. phēnix validates each config against the file of
its own `apiVersion`:

| `apiVersion` | Schema file | Kinds |
|---|---|---|
| `phenix.sandia.gov/v0` | `v0.yaml` | `Workflow` |
| `phenix.sandia.gov/v1` | `v1.yaml` | `Topology`, `Experiment`, `Image`, `User`, `Role` |
| `phenix.sandia.gov/v2` | `v2.yaml` | `Scenario` |

A config whose kind is not in the file of its `apiVersion`, such as a
`phenix.sandia.gov/v2` `Topology`, is rejected. A running phēnix server serves
each file at `GET /api/v1/schemas/{version}`, and the schema of one kind at
`GET /api/v1/schemas/{kind}/{version}`. The complete files are [shown
below](#complete-schemas).

## Topology Schema

The `topology` schema is largely comprised of nodes, [described
here](#node-schemas), and `nodes` is required. The schema does not check the
`includeTopologies` key described in [Configuration
Files](configuration.md#topology); phēnix reads it when it loads the topology.

```yaml
--8<-- "src/go/types/version/schemas/v1.yaml:topology"
```

## Scenario Schema

```yaml
--8<-- "src/go/types/version/schemas/v2.yaml:scenario"
```

## Experiment Schema

The `scenario` an experiment embeds uses the `experiment_scenario` schema in
`v1.yaml`, a copy of the [Scenario schema](#scenario-schema).

```yaml
--8<-- "src/go/types/version/schemas/v1.yaml:experiment"
```

## Image Schema

```yaml
--8<-- "src/go/types/version/schemas/v1.yaml:image"
```

## User Schema

```yaml
--8<-- "src/go/types/version/schemas/v1.yaml:user"
```

## Role Schema

```yaml
--8<-- "src/go/types/version/schemas/v1.yaml:role"
```

## Workflow Schema

The workflow config (`phenix.yml`) is validated against the `Workflow` schema
in schema version `v0`. It is documented, with every field, in
[Workflow](workflow.md#schema).

## Node Schemas

### `minimega_node` Schema

The `minimega_node` schema contains references to the `interface` schema [described
here](#interface-schema).

```yaml
--8<-- "src/go/types/version/schemas/v1.yaml:minimega-node"
```

### `external_node` Schema

```yaml
--8<-- "src/go/types/version/schemas/v1.yaml:external-node"
```

## Interface Schema

```yaml
--8<-- "src/go/types/version/schemas/v1.yaml:interfaces"
```

## Complete Schemas

### `v1.yaml`

```yaml
--8<--
src/go/types/version/schemas/v1.yaml
--8<--
```

### `v2.yaml`

```yaml
--8<--
src/go/types/version/schemas/v2.yaml
--8<--
```
