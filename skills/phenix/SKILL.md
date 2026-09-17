---
name: phenix
description: 'Guide for the phenix CLI and REST/web API used to build and run cyber ranges and cyber experiments on minimega: Topology, Scenario, and Experiment configs, disk images, SCORCH, writing phenix-app-<name> user apps, and API auth (X-Phenix-Auth-Token, 401s). This skill should be used when working with phenix, phēnix, SCEPTRE, cyber ranges or cyber experimentation, minimega VMs managed by phenix, or any `phenix` subcommand (config, experiment, vm, image, vlan, mm, settings, ui, util).'
license: GPL-3.0-only
---

# phenix CLI and Web API

phēnix (`phenix`) is a Go/Cobra CLI (and matching REST API) for defining, deploying,
and managing emulated cyber experiments/ranges on top of minimega. It composes
three config resources — **Topology**, **Scenario**, **Experiment** — plus
supporting resources — **Image**, **User**, **Role** — all stored as
versioned YAML/JSON "configs" (`apiVersion`/`kind`/`metadata`/`spec`, Kubernetes-style).

Detailed references and examples, loaded only when needed:

| Need | Reference |
|---|---|
| Global flags, every subcommand and flag | [`references/cli.md`](references/cli.md) |
| REST auth modes and the full route table | [`references/web-api.md`](references/web-api.md) |
| `runPeriodically`, `fromScenario`, app catalog | [`references/scenario.md`](references/scenario.md) |
| Node annotations read by the default apps | [`references/annotations.md`](references/annotations.md) |
| App environment variables | [`references/app-environment.md`](references/app-environment.md) |
| Copyable Topology and Scenario configs | [`examples/topology.yaml`](examples/topology.yaml), [`examples/scenario.yaml`](examples/scenario.yaml) |

## Core Concepts

### Config resource shape

Every phenix config (topology, scenario, experiment, image, user, role) is:

```yaml
apiVersion: phenix.sandia.gov/v1   # or v2 for some kinds (e.g. Scenario)
kind: Topology                     # Topology | Scenario | Experiment | Image | User | Role
metadata:
  name: my-topology
spec:
  ...
```

Configs are referenced everywhere as `<kind>/<name>` (lowercase kind), e.g.
`topology/foo`, `scenario/bar`, `experiment/foobar`.

### Topology

Defines the static network: a list of `nodes` (VMs, routers, firewalls, or
external/physical nodes) and their hardware, network interfaces, and boot
behavior. Key node fields (see [`examples/topology.yaml`](examples/topology.yaml)
for a complete, copyable two-node topology):

- `type`: `VirtualMachine | Firewall | Router | Switch`
- `general.hostname`, `general.vm_type` (`kvm` or `container`, default `kvm`),
  `general.do_not_boot`, `general.snapshot`
- `hardware.os_type`: `linux | windows | centos | rhel | minirouter | vyatta | vyos | other`
- `hardware.vcpus`, `hardware.memory`, `hardware.drives[].image` (disk image name/path)
- `network.interfaces[]`: `name`, `vlan`, `type: ethernet`, `proto: static|dhcp|manual`
  (router/firewall interfaces also accept `ospf`), `address`, `mask`, `gateway`, `bridge`
- `network.rulesets[]`: firewall rules with `action: accept|drop|reject`
- `injections[]` / `deletions[]`: files to inject into or remove from the disk image before boot
- `delay`: boot delay (timer, user-ack, or C2/miniccc-based)
- `annotations`: free-form hints for apps; the ones the default apps read are in
  [`references/annotations.md`](references/annotations.md)
- An `external` node represents a physical/non-minimega host in the topology (not scheduled/booted).
- `includeTopologies`: a top-level (spec-level, not per-node) list of other topology
  configs to merge into this one — entries can be a stored config name or a file path
  (e.g. `includeTopologies: ["/phenix/topologies/foo.yml", "bar-from-store"]`). Useful
  for sharing common node blocks (e.g. a standard firewall/router) across topologies.

For the full topology field-by-field reference (defaults, required values, delay
start, NAT, external node schema, hostname constraints), pull in
[Sceptre Phenix Topology Configuration](https://phenix.sceptre.dev/latest/configuration/#topology)
as needed rather than relying on the summary above.

### Scenario

Assigns **apps** to run against hosts in a topology, without hardcoding the
topology itself — the same scenario can be reused across topologies. Structure
(see [`examples/scenario.yaml`](examples/scenario.yaml) for a complete one):

```yaml
apiVersion: phenix.sandia.gov/v2
kind: Scenario
metadata:
  name: my-scenario
spec:
  apps:
    - name: my-app          # built-in or user app name
      assetDir: /path        # optional dir of app-specific assets
      metadata: {...}         # app-wide config, app-defined schema
      hosts:                  # optional; omit if the app has no per-host config
        - hostname: node1
          metadata: {...}     # per-host app config
      runPeriodically: 30s    # optional; only honored by the UI/API or `start --honor-run-periodically`
      disabled: false
```

Built-in apps `ntp`, `serial`, `startup`, and `vrouter` always run; every
other app is listed explicitly in `apps` (`phenix experiment apps` shows what
is available). `runPeriodically` semantics, `fromScenario` inheritance, and
the optional app catalog are in [`references/scenario.md`](references/scenario.md).

### Experiment

Combines a Topology + (optional) Scenario into a runnable, stateful
experiment. `phenix experiment create` copies the referenced topology and
scenario specs into the experiment config, so later edits to the stored
topology/scenario do not affect an existing experiment. Spec fields, with the
`experiment create` flag or command that sets each:

```yaml
apiVersion: phenix.sandia.gov/v1
kind: Experiment
metadata:
  name: my-exp
spec:
  experimentName: my-exp
  baseDir: /phenix/experiments/my-exp   # -d/--base-dir; experiment working directory
  defaultBridge: phenix                 # -b/--default-bridge; minimega bridge name
  topology: {...}                       # -t; embedded copy of the Topology spec
  scenario: {...}                       # -s; embedded copy of the Scenario spec
  vlans:
    aliases: {EXP-1: 101}               # phenix vlan alias
    min: 100                            # --vlan-min / phenix vlan range
    max: 200                            # --vlan-max
  schedules: {server-00: compute-01}    # phenix experiment schedule <exp> <algorithm>
  deployMode: all                       # inherited from --deploy-mode / config.yaml
  useGREMesh: false                     # inherited from --use-gre-mesh
```

Lifecycle: `create` → `schedule` (assign VMs to cluster hosts) → `start`
(deploy VMs via minimega) → `stop` → `restart`/`reconfigure` → `delete`.
An experiment also tracks runtime `status` (start time, per-VM schedule,
per-app state, allocated VLANs) once started. The same fields are editable
through `PATCH /api/v1/experiments/{name}` on a stopped experiment.

## CLI Overview

Command groups: `config` (stored configs), `experiment` (lifecycle), `vm`
(running VMs), `image` (vmdb2 disk images), `vlan` (per-experiment VLAN
aliases/ranges), plus `mm`, `settings`, `ui`, `util`, `completion`, and
`version`. Every subcommand accepts the persistent flags documented in
[`references/cli.md`](references/cli.md), which also lists each subcommand's
own flags.

A topology and scenario go from YAML to running VMs like this:

```bash
phenix config create topology.yaml scenario.yaml   # validates against the schema
phenix experiment create my-exp -t my-topology -s my-scenario
phenix experiment schedule my-exp round-robin      # assign VMs to cluster hosts
phenix experiment start my-exp                     # deploy via minimega
phenix vm info my-exp                              # verify VMs booted
phenix experiment trigger running my-exp           # re-fire a lifecycle stage on demand
phenix experiment stop my-exp
```

## Running phenix

**Docker (typical deployment):** phenix runs as the `phenix` container
alongside a `minimega` container (`docker/docker-compose.yml`). The `phenix ui`
process inside it is both the CLI binary and the web server, so CLI commands
are run with `docker exec` rather than a host-installed binary:

```bash
docker exec phenix phenix experiment list   # no -it for scripted/agentic use
```

For interactive human use, `make install-wrapper` installs
`scripts/phenix-wrapper.sh` as `/usr/local/bin/phenix` so shell completion and
TTY allocation work. See `README.md` ("Running with Docker").

**Local binary (development):** `make build` produces `bin/phenix`; run any
subcommand from it, typically against a local `bolt://` store and a local
`minimega`. See `README.md` ("Local Development") for prerequisites and
`make help` for targets.

## Querying the Web API

`phenix ui` serves the web UI and REST API on one port, `0.0.0.0:3000` by
default, with every route under `/api/v1`. Auth is off unless
`--jwt-signing-key` is set (it is empty by default and in the bundled compose
file), in which case the server serves every request as `global-admin` and
plain `curl http://localhost:3000/api/v1/experiments` works. When a key is
set, log in and send the JWT in the custom `X-Phenix-Auth-Token` header:

```bash
TOKEN=$(curl -s -u admin:password http://localhost:3000/api/v1/login | jq -r .token)
curl -H "X-Phenix-Auth-Token: $TOKEN" http://localhost:3000/api/v1/experiments
```

[`references/web-api.md`](references/web-api.md) has the route table, the
`?token=` query-parameter alternative, and proxy/dev auth modes.

## Gotchas

- **`phenix experiment create -t/-s` accept a stored config name or a file path.** When the
  value is a path (has a file extension), phenix runs `config create` with validation
  first, then uses the resulting config name — so a typo'd path silently becomes
  "config not found" further down the pipeline.
- **`vm_type` default is `kvm`, not `container`** — do not assume container semantics
  unless the topology explicitly sets `general.vm_type: container`.
- **Store endpoint changes the whole world.** `--store.endpoint` (bolt or etcd) determines
  which configs/experiments are visible — commands against the wrong endpoint will report
  "no configs found" rather than an obvious connection error.
- **Deleting `config.yaml` while phenix is running breaks the file watcher** (hot-reload of
  log level, deploy-mode, etc. stops working). `phenix settings unset <key>` rewrites the
  file in place and leaves the watcher intact; `phenix settings unset --all` deletes the
  file outright, so it has the same effect as removing it by hand and any running phenix
  process needs a restart afterwards.
- **Scenario `apiVersion` differs from Topology/Experiment** — Scenario currently uses
  `phenix.sandia.gov/v2` while Topology/Image typically use `v1`; mixing them up in a
  hand-written config causes schema validation failures.
- **`phenix config edit` on a running experiment's config requires `--force`** — edits are
  normally blocked once an experiment exists to avoid drift between the stored config and
  the deployed state.
- **`phenix experiment trigger-running` is deprecated** — use
  `phenix experiment trigger running <exp> [app ...]`, which also reaches the
  `configure`, `pre-start`, `post-start`, and `cleanup` stages.

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| `expects the configuration kind to be one of [...]` | Kind in `<kind>/<name>` is misspelled or unsupported. Kinds are case-insensitive but must be one of `topology, scenario, experiment, image, user, role` (plus `all` where supported). |
| `Unable to create configuration from <path>` | File isn't valid YAML/JSON, fails schema validation, or path doesn't exist. Try `--skip-validation` to isolate schema vs. parse errors. |
| Experiment `create` succeeds but `start` fails to boot VMs | Re-run with `phenix experiment start --dry-run <exp>` to see what would be sent to minimega, check `phenix vm info <exp>` and minimega directly via `phenix mm <cmd>`, and verify the disk images referenced in the topology exist (`phenix image list` for image configs, `GET /api/v1/disks` for disk files present on the headnode). |
| Web API calls return 401 | That server has a JWT signing key configured, so auth is on: send a current token in `X-Phenix-Auth-Token` (or `?token=`), not in `Authorization` — standard bearer-token tooling silently 401s. Re-login if the token expired. A server started without a signing key never returns 401. |
| `configuration not updated` after `phenix config edit` | No changes were saved in the editor — this is expected, not an error. |
| Settings changes via `phenix settings set` don't seem to apply | Command-line flags always win over the config file; unset the flag or use `phenix settings unset <key>` to fall back to the file/env value. |

## Writing phenix apps

A custom user app is an executable phenix shells out to. For phenix to find
it at all, it must meet three requirements (`src/go/app/doc.go`):

1. Be on the `PATH` of the process running phenix.
2. Be executable.
3. Be named `phenix-app-<name>`, where `<name>` is the app name used in the
   scenario's `apps[].name` field — a scenario app named `foo` resolves to the
   executable `phenix-app-foo`. A missing executable fails the stage with
   `external user app phenix-app-<name> does not exist in your path`.

Apps receive experiment JSON on stdin and one lifecycle argument, emit
experiment JSON on stdout and JSON logs on stderr, and exit 0 on success. A
non-zero exit fails the stage, except exit code 101, which reschedules the
experiment using the scheduler named on stdout and re-runs the app. Apps read
their configuration from the environment variables listed in
[`references/app-environment.md`](references/app-environment.md).

`phenix util app-json <exp>` prints the exact JSON a user app receives on stdin
for that experiment, which is the fastest way to test an app outside phenix:
`phenix util app-json my-exp | phenix-app-foo configure`. Runnable Go and
Python reference apps live in the repository's `examples/` directory
(`examples/go/main.go`, `examples/python/app.py`, with `examples/TUTORIAL.md`).

## Contributing

When changing this repository, read `AGENTS.md` at the repository root first;
its "Change Management" section sets branch, commit, changelog, and pull
request requirements. Behavior changes must be reflected in this skill.
