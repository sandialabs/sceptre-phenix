---
name: phenix
description: 'Guide for using the phenix CLI and REST/web API to build and run cyber ranges, cyber experiments, and network/system emulation environments. Covers Topology (nodes, VMs, networks), Scenario (apps assigned to hosts), and Experiment (topology + scenario deployed via minimega) resources. Use when asked about phenix, phēnix, SCEPTRE, cyber range, cyber experimentation, emulation, minimega, SCORCH, or any `phenix` subcommand (config, experiment, vm, image, vlan, mm, settings, ui).'
license: GPL-3.0-only (see LICENSE)
---

# phenix CLI and Web API

phēnix (`phenix`) is a Go/Cobra CLI (and matching REST API) for defining, deploying,
and managing emulated cyber experiments/ranges on top of minimega. It composes
three config resources — **Topology**, **Scenario**, **Experiment** — plus
supporting resources — **Image**, **User**, **Role** — all stored as
versioned YAML/JSON "configs" (`apiVersion`/`kind`/`metadata`/`spec`, Kubernetes-style).

## Running phenix

**Docker (typical deployment, preferred):** phenix normally runs as the
`phenix` container alongside a `minimega` container (see `docker/docker-compose.yml`
and the README's "Running with Docker" section). The `phenix ui` process inside
the container is both the CLI binary and the long-running web server — the
`phenix` CLI subcommands are invoked by `docker exec`-ing into the running
container, not by installing a separate binary on the host:

```bash
docker exec phenix phenix experiment list
```

(Omit `-it`/interactive-TTY flags for agentic/scripted use — they're only
needed for an interactive human shell.) For interactive human use, install
the bundled wrapper script instead of aliasing `docker exec` yourself, so
shell completion and TTY allocation work correctly:

```bash
make install-wrapper   # installs scripts/phenix-wrapper.sh to /usr/local/bin/phenix
source <(phenix completion bash)   # now works like a native binary
```

See `README.md` ("Running with Docker (Preferred)" and "Shell Completion")
and `scripts/phenix-wrapper.sh` for details.

**Local binary (development):** build and run `phenix` directly on the host
(no Docker), typically against a local `bolt://` store and a locally-running
`minimega`:

```bash
make build              # builds the phenix binary (see Makefile)
./phenix ui             # or run any phenix subcommand directly
```

See `README.md` ("Local Development" / "Build") for prerequisites (Go, Python,
Node.js/Yarn, protoc) and `make help` for the full list of dev targets.

## When to Use This Skill

- User asks to create/edit/deploy/start/stop a phenix experiment, topology, or scenario.
- User mentions a cyber range, cyber experimentation, network emulation, or minimega VMs.
- User wants to run `phenix` CLI commands or call the phenix web API (`/api/v1/...`).
- User asks about phenix apps (ntp, serial, startup, vrouter, SCORCH, user apps) or scheduling algorithms.
- User wants to build/manage phenix disk images (pair with the `phenix-image` skill
  for build scripts, overlays, and vmdb2 troubleshooting).

## Querying the Web API

`phenix ui` serves the web UI and REST API together on the same port —
`0.0.0.0:3000` by default (`ui.listen-endpoint` / `--listen-endpoint`,
`PHENIX_UI_LISTEN_ENDPOINT`). All API routes live under `/api/v1` on that
port (see the [Web API Reference](#web-api-reference) below for the route
table).

Typical flow with `curl` against a local/default deployment:

```bash
# 1. Log in to obtain a JWT (GET with basic auth, or POST with a JSON body)
TOKEN=$(curl -s -u admin:password http://localhost:3000/api/v1/login | jq -r .token)
# or: TOKEN=$(curl -s -X POST -d '{"username":"admin","password":"password"}' \
#            http://localhost:3000/api/v1/login | jq -r .token)

# 2. Use the token on every subsequent request via the custom auth header
curl -H "X-Phenix-Auth-Token: $TOKEN" http://localhost:3000/api/v1/experiments
```

If accessing phenix inside its Docker container from the host, the container
uses `network_mode: host` in the default compose file, so `localhost:3000`
on the host reaches it directly (no port mapping/publishing needed).

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
behavior. Key node fields:

- `type`: `VirtualMachine | Firewall | Router | Switch`. The `vrouter` app
  configures routing and rulesets only on `Router` and `Firewall` nodes whose
  `hardware.os_type` is `minirouter`, `vyatta`, or `vyos` (`linux` there is
  deprecated: it writes a Vyatta config into the image)
- `general.hostname`, `general.vm_type` (`kvm` or `container`, default `kvm`),
  `general.do_not_boot`, `general.snapshot`
- `hardware.os_type`: `linux | windows | centos | rhel | minirouter | vyatta | vyos`
- `hardware.vcpus`, `hardware.memory`, `hardware.drives[].image` (disk image name/path)
- `network.interfaces[]`: `name`, `vlan`, `type: ethernet`,
  `proto: static|ospf|dhcp|manual` (`manual` brings it up with no address),
  `address`, `mask`, `gateway`, `bridge`
- `network.rulesets[]`: firewall rules with `action: accept|drop|reject`
- `injections[]` / `deletions[]`: files to inject into or remove from the disk image before boot
- `delay`: boot delay (timer, user-ack, or C2/miniccc-based)
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
topology itself — the same scenario can be reused across topologies. Structure:

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
      runPeriodically: 30s    # optional; re-run app on an interval
      disabled: false
```

`fromScenario` lets one scenario inherit an app's `assetDir`/`metadata`/`hosts`/
`disabled`/`runPeriodically` from another stored scenario config by name
(`- name: my-app` + `fromScenario: other-scenario`). The referenced scenario
must have a `topology` annotation matching the topology this scenario is used
with, and must contain an app with the same `name`. This is how a shared
"base" scenario can be layered under per-experiment overrides without
duplicating app config.

Built-in apps that always run for every experiment: `ntp`, `serial`, `startup`,
`vrouter`. Additional/optional apps (e.g. `user-shell`, SCORCH, monitoring
apps like `packetbeat`/`elasticsearch`, `caldera`, `scale`, `wireguard`,
`otsim`, `helics`) are added explicitly in the scenario's `apps` list; many of
these live in the companion `sceptre-phenix-apps` repo (Python apps under
`phenix_apps/apps/`). List available apps with `phenix experiment apps`.

**Node annotations**: `annotations` is a free-form `map[string]any` on a
Topology node (`spec.nodes[].annotations`), used to pass out-of-band hints to
apps without adding first-class schema fields. Apps read them via
`node.GetAnnotation("key")` and interpret the value however they define it.
Annotations used by phenix's own default apps:

- `phenix/default-apps: false` — skips the `ntp`, `serial`, `startup`, and
  `vrouter` default apps for that specific node on every lifecycle stage,
  while still letting phenix/minimega manage the VM normally. User apps from
  the scenario are unaffected. Omit the annotation (or set it `true`) to keep
  the default behavior.
- `phenix/startup-autotunnel` (`startup` app, `post-start` stage) — a list of
  strings, each describing a port forward to auto-create for the node once it
  boots, e.g. `["8080", "8080:9090", "8080:10.0.0.5:9090"]`. Each entry is
  `sport[:dhost]:dport` — port-only forwards `sport` to `127.0.0.1:sport`;
  `sport:dport` forwards to `127.0.0.1:dport`; `sport:dhost:dport` forwards to
  an arbitrary destination host/port. Malformed entries are logged and skipped.
- `phenix/startup-via-cc` (`startup` app, `pre-start` stage) — when truthy,
  the generated startup scripts are delivered and executed over minimega C2
  (`send` plus `exec-once`) instead of being injected into the disk, and all
  startup-owned injections (including the Windows wrapper and Start Menu
  scheduler) are removed. C2 delivery is also used automatically when the
  node's first drive has `inject_partition: 0`, in which case the startup
  injections themselves are left in place.
- `vrouter/vyos-password` (`vrouter` app) — overrides the default `vyos`
  login password used when templating the boot config for a VyOS router node.
- `vrouter/enable-ssh` (`vrouter` app) — an interface name or IP address; when
  set, SSH access is enabled on the router templated to listen on that
  interface's address (or the literal IP given).

For the full scenario/app reference (per-app metadata schemas, `runPeriodically`
semantics, SCORCH scenario format), pull in
[Sceptre Phenix Scenario Configuration](https://phenix.sceptre.dev/latest/configuration/#scenario)
and [Apps](https://phenix.sceptre.dev/latest/apps/) as needed.

### Experiment

Combines a Topology + (optional) Scenario into a runnable, stateful
experiment with a base directory, VLAN pool, bridge name, and deploy mode.
Lifecycle: `create` → `schedule` (assign VMs to cluster hosts) → `start`
(deploy VMs via minimega) → `stop` → `restart`/`reconfigure` → `delete`.
An experiment also tracks runtime `status` (VM/app state) once started.

## CLI Reference

### Global flags and configuration precedence

Every subcommand accepts these persistent flags (bound to viper, so each also
has a matching config-file key and `PHENIX_*` env var):

| Flag | Config key / Env var | Default | Description |
|---|---|---|---|
| `--store.endpoint` | `store.endpoint` / `PHENIX_STORE_ENDPOINT` | `bolt:///etc/phenix/store.bdb` (root) or `bolt://~/.phenix.bdb` (non-root) | Data store endpoint (`bolt://...` or `etcd://host:port`; etcd accepts `?compaction-retention=<duration>`, default `1h`, `0` disables phenix's cluster-wide history compaction) |
| `--base-dir.phenix` | `base-dir.phenix` / `PHENIX_BASE_DIR_PHENIX` | `/phenix` | Base phēnix data directory |
| `--base-dir.minimega` | `base-dir.minimega` / `PHENIX_BASE_DIR_MINIMEGA` | `/tmp/minimega` | Base minimega directory |
| `--hostname-suffixes` | `hostname-suffixes` | `-minimega,-phenix` | Hostname suffixes to strip |
| `--log.level` | `log.level` / `PHENIX_LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` — use `--log.level=debug` for verbose troubleshooting output |
| `--log.console` | `log.console` / `PHENIX_LOG_CONSOLE` | `stderr` | Console log destination: `stderr`, `stdout`, or a file path |
| `--log.system.path` | `log.system.path` / `PHENIX_LOG_SYSTEM_PATH` | `/var/log/phenix/phenix.log` | Persistent JSON system log path (used by UI) |
| `--log.system.max-size` | `log.system.max-size` / `PHENIX_LOG_SYSTEM_MAX_SIZE` | `100` | Max log file size (MB) before rotation |
| `--log.system.max-backups` | `log.system.max-backups` / `PHENIX_LOG_SYSTEM_MAX_BACKUPS` | `3` | Number of rotated log files to retain |
| `--log.system.max-age` | `log.system.max-age` / `PHENIX_LOG_SYSTEM_MAX_AGE` | `90` | Max age (days) to retain old logs |
| `--bridge-mode` | `bridge-mode` | (unset) | `manual` (user/`phenix`-named bridge) or `auto` (experiment-named bridge) |
| `--deploy-mode` | `deploy-mode` | (unset) | `all`, `no-headnode`, or `only-headnode` — which minimega VMs to deploy |
| `--use-gre-mesh` | `use-gre-mesh` | `false` | Use GRE tunnels between mesh nodes for VLAN trunking |
| `--unix-socket` | `unix-socket` | `/tmp/phenix.sock` | Unix socket to listen on (`ui`) or connect to (other commands, to inherit server-set options) |

Precedence (highest to lowest): **1. command-line flag** → **2. `config.yaml`**
(managed with `phenix settings set`/`unset`, hot-reloaded) → **3. environment
variable** (`PHENIX_*`) → **4. built-in default**. See
[Settings & Configuration](https://phenix.sceptre.dev/latest/settings/) for the
full settings reference, including UI-only settings (`ui.logs.level`,
`ui.features`, `ui.file-server-endpoint`) not exposed as root-level CLI flags.

`phenix ui --features vm-mount` (equivalently `ui.features: vm-mount` in
`config.yaml`, or `PHENIX_UI_FEATURES=vm-mount`) enables the optional
"VM mount" UI feature, which lets users transfer files to and from a running
VM's filesystem directly from the web UI (backed by the `/experiments/{exp}/vms/{name}/mount`,
`/unmount`, `/files`, `/files/download`, `/files/upload` API routes).

`phenix ui --features builder-beta` enables Builder Flow, the Vue Flow
topology editor (a beta), at `/builder-beta` and its draft/document APIs. It
leaves the legacy `/builder` route available for `builder-xml` topologies.
Drafts autosave separately from phenix configs; only the explicit Publish action
creates or updates topology, scenario, or experiment configs. Its Router and
Firewall device templates create `minirouter` nodes (image `minirouter.qc2`)
of type `Router` and `Firewall`, which the `vrouter` app configures. The
Inspector suggests drive images, and the diagram checks flag a missing one,
from `GET /disks`; without the `disks` `list` permission it does neither.

Builder Flow works over plain HTTP as well as HTTPS. A published diagram
opens read only; Edit as a draft creates a draft from it (or reopens the draft
made from it before), which needs `configs` `create`, so a role with only
`list`/`get` can view drafts and published diagrams but cannot create, import,
upload, or publish. Configs' edit button for a Builder Flow topology links to
`/builder-beta?topology=<name>`, which opens the user's draft of it (making one
the first time) and then names it as `?draft=<owner>/<id>`, so a reload reopens
that draft; with the feature off, Configs explains that the topology can only
be edited in Builder Flow. The Inspector also edits a node's labels,
annotations, and advanced (minimega `vm config`) settings. Logging out removes
Builder Flow's local drafts (IndexedDB `phenix-builder`) and recent commands
from the browser, and keeps its preferences (`phenix.builder.theme`,
`phenix.builder.panes` and `phenix.builder.shortcuts` in localStorage).

Draft owners can manage their own drafts. Cross-user access uses the
`builder-drafts` RBAC resource with `{owner}/{draft-id}` resource names. A role
that may inspect and modify every draft needs an explicit policy like:

```yaml
- resources: [builder-drafts]
  resourceNames: ["*/*"]
  verbs: [list, get, update, delete]
```

`create` is deliberately not a `builder-drafts` verb: a draft is always created
for the authenticated user, never on somebody else's behalf. Resource names are
matched with `filepath.Match`, which does not match `/`, so a bare `"*"` never
matches a `{owner}/{draft-id}` name — use `"*/*"` (as `global-admin` does) or an
explicit `alice/*`.

Every Builder Flow request also needs the base `configs` permission of the verb
it performs (`list`, `get`, `create`, `update`, `delete`), so builder access can
never exceed a user's config access. A cross-user request that fails the
`builder-drafts` check is answered with `404`, not `403`, so draft existence is
never disclosed. Every mutation after creation requires an `If-Match` header
carrying the quoted ETag the previous response returned: a missing or malformed
tag is `400`, a stale one `412`. Draft responses also carry the tag in their
body as `etag`; prefer it, since a compressing proxy can rewrite the header
(`W/"3"`, `"3-gzip"`). A `412` on `DELETE` carries the draft's current
`ETag`, since a draft whose metadata this server can no longer read (written by
a newer phenix, say) is left out of listings and cannot be read, but its owner
can still delete it.

A snapshot append (`POST /builder/drafts/{owner}/{draft}/snapshots`) may carry
`opId`, the client's id for the save (1-128 letters, digits, `.`, `-`, `_`,
starting with a letter or digit). The snapshot manifest keeps it, and snapshot
listings return it, so a client whose response was lost can tell the snapshot
was stored. A draft keeps at most 50 snapshots and 50 MiB of them; past either,
the oldest are dropped. The UI keeps unsaved Builder Flow edits in the browser's
`phenix-builder` IndexedDB database only until the server confirms them.

Publishing still requires the applicable config, scenario, and experiment
permissions; Builder draft access does not bypass them.

A mutation whose durable write succeeded but whose superseded content could not
be removed returns its normal success status, body, and new `ETag`, plus a
`Warning: 199` header naming the operation; the cause is logged, never sent.
Failing such a request would only make the client retry with a stale tag.

`GET /builder/sources` groups configs by kind: `topologies` and `experiments`
(what a document can be generated from, reported as `generatable: true`),
`scenarios` (selectable when publishing) and `images` (node property editing).
Each config is filtered through the `configs` permission *and* the kind specific
`list` permission that already gates the kind elsewhere (`topologies`,
`experiments`, `scenarios`); `Image` configs have no kind specific vocabulary,
so `configs` is their only gate. Generating from a non-generatable kind is
`422`. VLANs are derived from the document and are not a config kind.

`POST /builder/generate` accepts either `{"source":"Topology/name"}` (or an
Experiment source) or `{"content":"..."}` containing an uploaded JSON/YAML
Topology or Experiment. Uploaded sources are reported as `stored: false` and
receive uploaded provenance, so they can create publication targets but cannot
authorize a Topology or Experiment update. Uploaded `content` needs `configs`
`create` (a stored `source` needs only read permissions): uploads are parsed
like `POST /configs`, including `${NAME}` / `${NAME:default}` substitution from
the server's environment, so anyone allowed to create configs can read the
server's environment variables (sandialabs/sceptre-phenix#436 describes this).

Generation resolves a Topology's `includeTopologies` recursively, the way
phenix merges them, and adds the included nodes as devices marked
`device.includedFrom: <defining topology>`; for an Experiment, whose topology
phenix already merged, the included nodes are recognized and marked instead
(a hostname the experiment's own topology defines is never marked). When an
include cannot be read now, a node that neither the experiment's own topology
nor a readable include defines is marked as coming from it (from the first,
when several cannot be read).
Includes are read from the config store only (never file paths), under the
caller's `configs` `get` and `topologies` `list` permissions; a missing,
forbidden, cyclic, or repeated include, or a hostname that collides with
another topology's, is reported as a warning. Included devices are read only in
the editor (they can be moved, not changed, deleted, or reconnected), and
publishing omits them and writes `includeTopologies` instead, so a round trip
does not duplicate them. The exception is an Experiment whose own topology
and one of whose includes both cannot be read: that include's nodes cannot be
told from the topology's own, so they are imported as its own (with a warning)
and a topology publish copies them in, where phenix finds them twice. Publish
answers 409 when an included topology now defines a hostname the published
topology also defines, and an experiment update, which merges the includes
itself, answers 403 or 422 for an include the caller may not read or that is
not a stored topology.

A stored scenario reference carries the config's `apiVersion` and content
`digest` as `GET /builder/sources` lists them, never its content. Generating
from a stored Experiment whose `scenario` annotation names a Scenario the
caller may list produces such a reference, so the draft publishes back with
scenario action `use`. If that Scenario is missing or hidden from the caller,
or the Experiment was uploaded, the Experiment's embedded copy is attached as
an uploaded scenario of the same name, with a warning: an uploaded Experiment
is never bound to this server's Scenario of that name, which may differ.

Publishing an uploaded Scenario with action `update` requires
`scenario.expectedDigest`, copied from a fresh matching entry returned by
`GET /builder/sources`. A digest mismatch is a conflict; never retry it with a
guessed digest. Topology and Experiment updates likewise require a draft tied
to that exact stored source: one imported from it, or one that published it (or
was opened from the published diagram that did), with nothing else having
changed it since. Otherwise the update gets 409, for example `topology <name>
changed after this draft published it` or `experiment <name> changed after this
draft published it`. A published topology names its document in its
`builder-doc` annotation; a published experiment records the draft and document
that published it, and its digest after the configure stage, in its
`builder-experiment` annotation, so any later change to its spec counts. An
Experiment update then runs the apps' configure stage, as
`PUT /configs` does. A failed configure stage leaves the experiment unchanged
and is reported as a `partial` result, and an experiment found running once its
lock is held is too. An Experiment create is refused with 422 before anything is
written if its name is `all` (in any case), or longer than 15 characters in auto
bridge mode. A name outside the config naming rule is refused with 400.

Publish answers 422 when an interface of a device that is not external has no
VLAN, and the error `message` names the devices and interfaces (the first three,
then how many more; by position, such as `#2`, when unnamed or when two share a
name): phenix would store such a topology, but minimega refuses the interface
when the experiment starts. Connect the interface or give it a VLAN. A VLAN
that names no network of the document still publishes as it is, since phenix
allocates VLANs by name and matches them exactly (`exp` is not network `EXP`).
Drafts keep such interfaces; the editor flags them as warnings.

Features are disabled by default and require restarting `phenix ui` after
changing `ui.features`.

### `phenix config` — manage stored configs (topology/scenario/experiment/image/user/role)

```bash
phenix config list <kind|all>                      # table of stored configs
phenix config get <kind>/<name> [-o yaml|json] [-p] # dump a config
phenix config create </path/to/file.yaml> ...       # create from file(s) or dir; validates against schema
phenix config create --skip-validation <file>       # skip schema validation
phenix config edit <kind>/<name> [--force]          # open in $EDITOR
phenix config delete <kind>/<name> ...              # delete one or more specific configs by kind/name
phenix config delete all [kind]                     # delete every stored config, or every config of one kind
```

### `phenix experiment` — experiment lifecycle

```bash
phenix experiment list
phenix experiment apps                              # list available apps
phenix experiment schedulers                        # list scheduling algorithms
phenix experiment create <exp> -t <topology> [-s <scenario>] [-d <base-dir>] \
  [--disabled-apps app1,app2] [--vlan-min N] [--vlan-max N] [-b <bridge>]
phenix experiment edit <exp>
phenix experiment delete <exp>
phenix experiment schedule <exp> <algorithm>         # e.g. round-robin, isolate-experiment, subnet-compute
phenix experiment start <exp>
phenix experiment stop <exp>
phenix experiment restart <exp>
phenix experiment reconfigure <exp>
phenix experiment trigger-running <exp> [app ...]    # re-fire "running" stage for app(s)
phenix experiment scorch <exp>                       # run a SCORCH pipeline for the experiment
```

`-t`/`-s` accept either the name of an already-stored config or a path to a
YAML/JSON file (in which case it's auto-created as a config first).

### `phenix vm` — manage running VMs within an experiment

```bash
phenix vm info <exp> [vm]                            # table of VM(s)
phenix vm pause|resume|restart|shutdown|kill <exp> [vm]
phenix vm reset-disk <exp> [vm]
phenix vm redeploy <exp> [vm]
phenix vm set <exp> [vm] --<key> <value>             # change VM config (cpu, memory, disk, etc)
phenix vm net connect <exp> <vm> <iface index> <vlan id>
phenix vm net disconnect <exp> <vm> <iface index>
phenix vm capture start <exp> <vm> <iface index> <output file>
phenix vm capture start-subnet <exp> <subnet>
phenix vm capture stop <exp> <vm>
phenix vm capture stop-subnet <exp> <subnet>
phenix vm capture stop-all <exp>
phenix vm memory-snapshot <exp> <vm> <path>
```

### `phenix image` — disk image (vmdb2) configuration and builds

```bash
phenix image list
phenix image create <name> [-s 10G] [-v minbase] [-r jammy] [-m <mirror>] \
  [-f qcow2] [-c] [-R] [-O overlay1,overlay2] [-P pkg1,pkg2] [-T script1,script2] \
  [-k arg1,arg2] [--skip-default-pkgs] [--no-virtuals]
phenix image create-from <existing> <new>
phenix image edit <name>
phenix image build <name>
phenix image delete <name>
phenix image append|remove|update <name> ...
phenix image inject-miniexe <path/to/exe> <path/to/disk>
```

Default variant/release/mirror are `minbase`/`jammy`/Ubuntu archive; `-f`
supports `qcow2` (default) and other formats vmdb2 supports. For build scripts,
overlays, `Image` config fields, vmdb2 environment, and build troubleshooting,
read the sibling [`phenix-image`](../phenix-image/SKILL.md) skill.

### `phenix vlan` — VLAN aliasing/ranges per experiment

```bash
phenix vlan alias <exp> <alias name> <vlan id>   # view (no value) or set an alias
phenix vlan range <exp> <range min> <range max>  # view or set the VLAN pool range
```

### Other subcommands

```bash
phenix mm <minimega args>...     # pass raw commands to (or attach to) minimega
phenix settings list|get|set|unset [key] [value]
phenix settings db ...            # legacy BoltDB-backed settings
phenix ui                         # run the phenix web UI/API server
phenix completion bash|zsh|fish|powershell
phenix version
```

## Web API Reference

Base path: `/api/v1`. Auth: `X-Phenix-Auth-Token: <jwt>` header (obtained via
`POST /api/v1/login`), NOT the standard `Authorization` header. All routes
below are relative to the base path.

| Resource | Routes |
|---|---|
| Configs | `GET/POST /configs`, `GET/PUT/DELETE /configs/{kind}/{name}`, `POST /configs/download` |
| Schemas | `GET /schemas/{version}`, `GET /schemas/{kind}/{version}` (404 for an unknown kind or version) |
| Experiments | `GET/POST /experiments`, `DELETE /experiments/{name}`, `GET /experiments/{name}/topology`, `POST /experiments/{name}/trigger`, `GET/POST /experiments/{name}/schedule`, `GET /experiments/{name}/soh` (state of health) |
| VMs | `GET/PATCH /experiments/{exp}/vms`, `GET/PATCH/DELETE /experiments/{exp}/vms/{name}`, plus `/start`, `/stop`, `/restart`, `/redeploy`, `/shutdown`, `/reset`, `/vnc`, `/vnc/ws`, `/screenshot.png`, `/captures`, `/snapshots`, `/commit`, `/memorySnapshot`, `/forwards` |
| Disks | `GET/POST/DELETE /disks`, `/disks/snapshot`, `/disks/rebase`, `/disks/resize`, `/disks/commit`, `/disks/clone`, `/disks/rename`, `/disks/download` |
| Misc lookups | `GET /vms` (all experiments), `GET /applications`, `GET /topologies`, `GET /topologies/{topo}/scenarios`, `GET /hosts` |
| Users/Roles/Auth | `GET/POST /users`, `GET/PATCH/DELETE /users/{username}`, `POST /users/{username}/tokens`, `GET /roles`, `POST /signup`, `GET/POST /login`, `GET /logout` |
| Realtime | `GET /ws` (websocket broker for UI events/logs), `GET /logs` |
| SCORCH | `/experiments/{name}/scorch/terminals*`, `/experiments/{name}/scorch/components/.../ws` |
| Settings | `GET/POST /settings`, `GET /settings/password` |
| Builder | `GET /builder`, `POST /builder/save`, `GET /builder/topologies[/{name}]` |
| Builder Flow (`builder-beta` feature only) | `GET /schemas/builder/v1`, `GET/POST /builder/drafts`, `GET/DELETE /builder/drafts/{owner}/{draft}`, `GET/POST /builder/drafts/{owner}/{draft}/snapshots`, `GET /builder/drafts/{owner}/{draft}/snapshots/{snapshot\|current}`, `PATCH/PUT /builder/drafts/{owner}/{draft}/cursor`, `POST /builder/drafts/{owner}/{draft}/publish`, `GET /builder/sources`, `POST /builder/generate`, `GET /builder/documents[/{document}]` |
| Options | `GET /options` (server-side CLI defaults like bridge-mode/deploy-mode) |

Unmatched `/api/v1/*` requests return a JSON `404`; only non-API routes fall
through to the SPA index. A route behind a disabled feature flag is therefore a
real `404`, not `200 text/html`.

Prefer the equivalent `phenix` CLI command over calling the web API directly
unless the user explicitly needs the HTTP interface (e.g. scripting against a
running `phenix ui` server, or building a UI integration).

## Gotchas

- **Kind names in `config`/`experiment` commands are case-insensitive but must be one of**
  `topology, scenario, experiment, image, user, role` (plus `all` where supported) —
  anything else errors before hitting the store.
- **`phenix experiment create -t/-s` auto-creates configs from file paths.** If you pass a
  path (has a file extension) instead of an existing config name, it runs `config create`
  with validation first, then uses the resulting config name — so a typo'd path silently
  becomes "config not found" further down the pipeline.
- **`vm_type` default is `kvm`, not `container`** — don't assume container semantics unless
  the topology explicitly sets `general.vm_type: container`.
- **Auth uses a custom header, not `Authorization`.** Web API calls must use
  `X-Phenix-Auth-Token`; standard bearer-token tooling will silently 401.
- **Store endpoint changes the whole world.** `--store.endpoint` (bolt or etcd) determines
  which configs/experiments are visible — commands against the wrong endpoint will report
  "no configs found" rather than an obvious connection error. An etcd store is compacted by
  phenix every retention/10 unless `compaction-retention=0`; then the operator must run etcd
  with auto-compaction.
- **Deleting `config.yaml` while phenix is running breaks the file watcher** (hot-reload of
  log level, deploy-mode, etc. stops working). Use `phenix settings unset --all` instead of
  removing the file.
- **Scenario `apiVersion` differs from Topology/Experiment** — Scenario currently uses
  `phenix.sandia.gov/v2` while Topology/Image typically use `v1`; mixing them up in a
  hand-written config causes schema validation failures.
- **`phenix config edit` on a running experiment's config requires `--force`** — edits are
  normally blocked once an experiment exists to avoid drift between the stored config and
  the deployed state.

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| `expects the configuration kind to be one of [...]` | Kind in `<kind>/<name>` argument is misspelled or unsupported; check spelling against the list above. |
| `Unable to create configuration from <path>` | File isn't valid YAML/JSON, fails schema validation, or path doesn't exist. Try `--skip-validation` to isolate schema vs. parse errors. |
| Experiment `create` succeeds but `start` fails to boot VMs | Check `phenix vm info <exp>` and minimega directly via `phenix mm <cmd>`; also verify disk images referenced in the topology exist (`phenix image list`, `phenix disk` API). |
| Web API calls return 401 | Confirm you're sending `X-Phenix-Auth-Token`, obtained fresh from `POST /api/v1/login`, not a stale token or `Authorization` header. |
| `configuration not updated` after `phenix config edit` | No changes were saved in the editor — this is expected, not an error. |
| Settings changes via `phenix settings set` don't seem to apply | Command-line flags always win over the config file; unset the flag or use `phenix settings unset <key>` to fall back to the file/env value. |

## Environment Variables (phenix apps)

When phenix launches an app (built-in or user app from a scenario), it sets
environment variables the app subprocess reads for its configuration — these
matter when writing or debugging a phenix app, not when just running the CLI:

| Environment Variable | Value | Description |
|---|---|---|
| `PHENIX_DIR` | `base-dir.phenix` | Base phēnix data directory |
| `PHENIX_FILES_DIR` | experiment files directory | Where the app reads/writes experiment files |
| `PHENIX_LOG_LEVEL` | phēnix's own value, else `DEBUG` | App log verbosity |
| `PHENIX_LOG_FILE` | `stderr` | App logs stream back to phēnix rather than to a file |
| `PHENIX_DRYRUN` | `true`/`false` | Whether the run is a dry run |
| `PHENIX_STORE_ENDPOINT` | `store.endpoint` | Data store endpoint (user apps only) |
| `PHENIX_SCORCH_STARTTIME` | run start time | SCORCH components only |
| `PHENIX_TEMP_DIR` | `/tmp/phenix` | App temporary directory (inherited from phenix process) |
| `MM_FILEPATH` | `/phenix/images` | Base minimega file path (inherited) |
| `MM_SOCKET_PATH` | `/tmp/minimega/minimega` | minimega command socket (inherited) |

Apps drive VMs over minimega's `cc` (command-and-control/miniccc) channel;
these bound how long an app waits (seconds) and are also inherited from the
phenix process:

| Environment Variable | Default | Description |
|---|---|---|
| `PHENIX_CC_POLL_RATE` | `2.0` | Interval between `cc` polls |
| `PHENIX_CC_CLIENT_GRACE` | `300.0` | Wait for a miniccc client to register before failing |
| `PHENIX_CC_SEND_GRACE` | `300.0` | Wait for a file send or component start to be acknowledged |
| `PHENIX_CC_CMD_GRACE` | `0.0` | Wait for a command response (`0` waits indefinitely, supervised by client liveness) |
| `PHENIX_CC_EXITCODE_GRACE` | `10.0` | Wait for an exit code once the response is counted |
| `PHENIX_CC_LIVENESS_INTERVAL` | `10.0` | Interval between client-liveness checks during an unbounded wait |
| `PHENIX_CC_LOG_INTERVAL` | `10.0` | Delay before the first "still waiting" log line |
| `PHENIX_CC_LOG_MAX_INTERVAL` | `320.0` | Ceiling for that interval, which doubles each time |

See [phēnix Apps Environment Variables](https://phenix.sceptre.dev/latest/settings/#phenix-apps-environment-variables)
for the authoritative reference.

## Contributing

Follow [`AGENTS.md`](../../AGENTS.md#change-management) for repository change,
documentation, branch, commit, and pull request requirements.

## References

See [`AGENTS.md`](../../AGENTS.md#documentation-and-references) for the
repository documentation map and companion phēnix repositories.
