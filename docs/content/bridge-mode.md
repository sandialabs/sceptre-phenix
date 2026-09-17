# Bridge Mode

phēnix uses [Open vSwitch (OVS)](https://www.openvswitch.org/) bridges managed by
minimega to connect VMs to virtual networks. The **bridge mode** setting controls
how phēnix names the default OVS bridge assigned to each experiment.

## Modes

### `manual` (Default)

In `manual` mode, the bridge name for an experiment comes from the default
bridge name supplied when the experiment is **created** — the `--default-bridge`
flag on `phenix experiment create`, the `Default Bridge Name` field in the UI's
`Options` section, or the `defaultBridge` key in a workflow file. It is a
property of the experiment, not of the topology it uses. If no bridge name is
provided, phēnix falls back to the shared default bridge named **`phenix`**.

This means that unless you explicitly assign a unique bridge name when creating
an experiment, all experiments using `manual` mode that have no custom bridge
name configured will share the same `phenix` bridge.

### `auto`

In `auto` mode, phēnix automatically assigns each experiment's own name as its
default bridge name. This gives every experiment a unique bridge without any
manual configuration, and the `Default Bridge Name` field is hidden in the UI's
experiment creation form because it would have no effect.

!!! warning
   Because OVS bridge names are limited to **15 characters**, experiment names
    must be 15 characters or fewer when using `auto` bridge mode. phēnix will
    return an error if you attempt to create or update an experiment with a
    longer name while `auto` mode is active.

## Bridge Names and Topologies

The experiment's default bridge is applied to every node interface in the
topology that does not name a bridge of its own. An interface that explicitly
sets a `bridge` other than `phenix` in the topology keeps that value, in both
`manual` and `auto` mode. Bear this in mind when using `auto` mode for
isolation: topology-pinned bridges are left untouched and can still be shared
between experiments.

## Bridge Name Collisions

Two experiments may not share the same default bridge name unless that name is
the shared `phenix` bridge. Creating or updating an experiment whose default
bridge is already claimed by another experiment fails with:

```text
experiment <other-experiment> already using default bridge <bridge-name>
```

Because `auto` mode derives the bridge name from the (necessarily unique)
experiment name, this collision cannot occur in `auto` mode.

## Why Bridge Mode Matters

Giving each experiment a unique bridge name has two important benefits:

1. **Network isolation** — Experiments sharing the `phenix` bridge are kept
   apart by VLAN alone. Giving each experiment its own bridge adds a second
   layer of separation, so a VLAN misconfiguration or overlapping VLAN range
   cannot bridge traffic between two experiments on the same host.

2. **Netflow capture** — phēnix's [netflow](netflow.md) feature only works for
   experiments that have a bridge name other than the default `phenix` bridge.
   Using `auto` mode (or manually specifying a unique bridge name per
   experiment) is therefore a prerequisite for per-experiment netflow capture.

## Configuring Bridge Mode

Bridge mode is a **global** setting applied at the phēnix server level. It can
be set in three ways, listed from highest to lowest precedence:

### 1. Command Line Flag

`--bridge-mode` is a global flag accepted by any phēnix subcommand, but it is
only consulted when an experiment is **created or updated**. Passing it to
`phenix exp start` or any other subcommand has no effect, because the bridge
name was already fixed when the experiment was created.

```bash
# Run the UI server in auto mode; every experiment created through it gets a
# bridge named after the experiment.
phenix ui --bridge-mode auto
```

```bash
# Create a single experiment in auto mode from the CLI.
phenix experiment create my-exp -t my-topology --bridge-mode auto
```

### 2. Configuration File

Add `bridge-mode` to the phēnix configuration file (see
[Settings & Configuration](settings.md) for file locations):

```yaml
bridge-mode: auto
```

### 3. Environment Variable

```bash
export PHENIX_BRIDGE_MODE=auto
phenix ui
```

## Example Workflow

The following shows how to start the phēnix UI server in `auto` mode so that
all experiments automatically receive unique bridges:

```bash
phenix ui --bridge-mode auto
```

With the server running in `auto` mode, creating a new experiment requires the
name to be 15 characters or fewer:

```bash
# Works — name is within the 15-character limit
phenix experiment create my-exp -t my-topology

# Fails — name is too long for auto bridge mode
phenix experiment create my-very-long-experiment-name -t my-topology
```

## Relationship to Netflow

The [netflow](netflow.md) feature captures network flows from the OVS bridge
used by an experiment, but it explicitly excludes the shared default `phenix`
bridge to prevent flows from multiple experiments being mixed together.

Setting bridge mode to `auto` is the simplest way to satisfy this requirement
across all experiments. Alternatively, in `manual` mode you can specify a
unique `--default-bridge` name per experiment at creation time:

```bash
phenix experiment create my-exp -t my-topology --default-bridge my-exp-br
```

See the [Netflow](netflow.md) page for more details on enabling and using
netflow capture.

## Settings Reference

| Setting Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `bridge-mode` | `PHENIX_BRIDGE_MODE` | `manual` | Bridge naming mode for experiments. `auto` uses the experiment name as the bridge name; `manual` uses the user-specified bridge name, or `phenix` if not specified. |
