# Configuration File Schemas

The following schemas are all represented using the `OpenAPIv3` format. Each
one is included from the schema files phēnix validates configs with
(`src/go/types/version/schemas/`), for the `apiVersion` phēnix stores that kind
in: `phenix.sandia.gov/v2` for Scenario, and `phenix.sandia.gov/v1` for
Topology, Experiment, Image, User, Role and the node and interface schemas. The
complete `phenix.sandia.gov/v2` schema is [included at the
end](#complete-schema).

## Topology Schema

The `topology` schema is largely comprised of nodes, [described
here](#node-schemas).

```yaml
--8<--
src/go/types/version/schemas/v1.yaml:Topology
--8<--
```

## Scenario Schema

```yaml
--8<--
src/go/types/version/schemas/v2.yaml:Scenario
--8<--
```

## Experiment Schema

```yaml
--8<--
src/go/types/version/schemas/v1.yaml:Experiment
--8<--
```

## Image Schema

```yaml
--8<--
src/go/types/version/schemas/v1.yaml:Image
--8<--
```

## User Schema

```yaml
--8<--
src/go/types/version/schemas/v1.yaml:User
--8<--
```

## Role Schema

```yaml
--8<--
src/go/types/version/schemas/v1.yaml:Role
--8<--
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
--8<--
src/go/types/version/schemas/v1.yaml:minimega_node
--8<--
```

### `external_node` Schema

```yaml
--8<--
src/go/types/version/schemas/v1.yaml:external_node
--8<--
```

## Interface Schema

```yaml
--8<--
src/go/types/version/schemas/v1.yaml:iface
src/go/types/version/schemas/v1.yaml:iface_address
src/go/types/version/schemas/v1.yaml:iface_rulesets
src/go/types/version/schemas/v1.yaml:static_iface
src/go/types/version/schemas/v1.yaml:dhcp_iface
src/go/types/version/schemas/v1.yaml:serial_iface
--8<--
```

## Complete Schema

The complete `phenix.sandia.gov/v2` schema file:

```yaml
--8<--
src/go/types/version/schemas/v2.yaml
--8<--
```
