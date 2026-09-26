# phenix CLI Reference

Full subcommand and flag reference. The [skill body](../SKILL.md) covers the
concepts and the commands needed for common workflows.

## Global flags and configuration precedence

Every subcommand accepts these persistent flags (bound to viper, so each also
has a matching config-file key and `PHENIX_*` env var):

| Flag | Config key / Env var | Default | Description |
|---|---|---|---|
| `--store.endpoint` | `store.endpoint` / `PHENIX_STORE_ENDPOINT` | `bolt:///etc/phenix/store.bdb` (root) or `bolt://~/.phenix.bdb` (non-root) | Data store endpoint (`bolt://...` or `etcd://host:port`; etcd accepts `?compaction-retention=<duration>`, default `1h`, `0` disables phenix's cluster-wide history compaction) |
| `--base-dir.phenix` | `base-dir.phenix` / `PHENIX_BASE_DIR_PHENIX` | `/phenix` | Base phēnix data directory |
| `--base-dir.minimega` | `base-dir.minimega` / `PHENIX_BASE_DIR_MINIMEGA` | `/tmp/minimega` | Base minimega directory |
| `--mount-dir` | `mount-dir` / `PHENIX_MOUNT_DIR` | `<base-dir.phenix>/mounts` | Base directory for VM filesystem mounts (`phenix vm mount`, UI `vm-mount` feature) |
| `--hostname-suffixes` | `hostname-suffixes` / `PHENIX_HOSTNAME_SUFFIXES` | `-minimega,-phenix` | Hostname suffixes to strip |
| `--log.level` | `log.level` / `PHENIX_LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` — use `--log.level=debug` for verbose troubleshooting output |
| `--log.console` | `log.console` / `PHENIX_LOG_CONSOLE` | `stderr` | Console log destination: `stderr`, `stdout`, or a file path |
| `--log.system.path` | `log.system.path` / `PHENIX_LOG_SYSTEM_PATH` | `/var/log/phenix/phenix.log` | Persistent JSON system log path (used by UI) |
| `--log.system.max-size` | `log.system.max-size` / `PHENIX_LOG_SYSTEM_MAX_SIZE` | `100` | Max log file size (MB) before rotation |
| `--log.system.max-backups` | `log.system.max-backups` / `PHENIX_LOG_SYSTEM_MAX_BACKUPS` | `3` | Number of rotated log files to retain |
| `--log.system.max-age` | `log.system.max-age` / `PHENIX_LOG_SYSTEM_MAX_AGE` | `90` | Max age (days) to retain old logs |
| `--bridge-mode` | `bridge-mode` / `PHENIX_BRIDGE_MODE` | (unset) | `manual` (user/`phenix`-named bridge) or `auto` (experiment-named bridge) |
| `--deploy-mode` | `deploy-mode` / `PHENIX_DEPLOY_MODE` | (unset) | `all`, `no-headnode`, or `only-headnode` — which minimega VMs to deploy |
| `--use-gre-mesh` | `use-gre-mesh` / `PHENIX_USE_GRE_MESH` | `false` | Use GRE tunnels between mesh nodes for VLAN trunking |
| `--unix-socket` | `unix-socket` / `PHENIX_UNIX_SOCKET` | `/tmp/phenix.sock` | Unix socket to listen on (`ui`) or connect to (other commands, to inherit server-set options) |

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
`/unmount`, `/files`, `/files/download`, `/files/upload` API routes). It's
disabled by default and requires restarting `phenix ui` to take effect. The CLI
equivalents (`phenix vm mount`/`unmount`) are always available.

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

## `phenix config` — manage stored configs (topology/scenario/experiment/image/user/role)

```bash
phenix config list <kind|all>                      # table of stored configs
phenix config get <kind>/<name> [-o yaml|json] [-p] # dump a config
phenix config create </path/to/file.yaml> ...       # create from file(s) or dir; validates against schema
phenix config create --skip-validation <file>       # skip schema validation
phenix config edit <kind>/<name> [--force]          # open in $EDITOR
phenix config delete <kind>/<name> ...              # delete one or more specific configs by kind/name
phenix config delete all [kind]                     # delete every stored config, or every config of one kind
```

## `phenix experiment` — experiment lifecycle

```bash
phenix experiment list
phenix experiment apps                              # list available apps
phenix experiment schedulers                        # list scheduling algorithms
phenix experiment create <exp> -t <topology> [-s <scenario>] [-d <base-dir>] \
  [--disabled-apps app1,app2] [--vlan-min N] [--vlan-max N] [-b <bridge>]
phenix experiment edit <exp> [--force]              # --force overrides the running-experiment check
phenix experiment delete <exp> [-f|--force]          # -f stops a running experiment before deleting it
phenix experiment schedule <exp> <algorithm>         # e.g. round-robin, isolate-experiment, subnet-compute
phenix experiment start <exp|all> [--dry-run] [--honor-run-periodically] \
  [--treat-mm-errors-as-warnings] [--vlan-min N] [--vlan-max N]
phenix experiment stop <exp>
phenix experiment restart <exp> [--dry-run]
phenix experiment reconfigure <exp>
phenix experiment trigger <lifecycle> <exp> [app ...]  # re-fire a lifecycle stage for app(s)
phenix experiment scorch <exp> [-r|--run <id>]      # start SCORCH run <id> (default 0) for the experiment
```

`-t`/`-s` accept either the name of an already-stored config or a path to a
YAML/JSON file (in which case it's auto-created as a config first).

`start all` starts every stopped experiment. `--dry-run` (also on `restart`)
does everything except call out to minimega, which isolates config and
scheduling problems from minimega problems. `--honor-run-periodically` keeps
the process in the foreground re-running `running` stages on their
`runPeriodically` interval.

`trigger` takes the lifecycle stage as its first argument; each stage has a
shorthand: `configure` (`config`), `pre-start` (`pre`), `post-start` (`post`),
`running` (`run`), `cleanup` (`clean`). Passing `all` instead of an experiment
name triggers the stage in every experiment, and passing no app names triggers
every app. The `running` stage is skipped for stopped experiments. Triggering
`configure` is not equivalent to `phenix experiment reconfigure`: it does not
validate the experiment config, run config hooks, reset the minimega bridge, or
guard against running experiments.

```bash
phenix experiment trigger running my-exp             # every app's running stage
phenix experiment trigger post my-exp startup        # post-start stage for one app
```

The older `phenix experiment trigger-running <exp> [app ...]` still exists but
is deprecated in favor of `phenix experiment trigger running <exp> [app ...]`.

## `phenix vm` — manage running VMs within an experiment

```bash
phenix vm info <exp> [vm]                            # table of VM(s)
phenix vm pause|resume|restart|shutdown|kill <exp> [vm]
phenix vm reset-disk <exp> [vm]
phenix vm redeploy <exp> [vm] [-c <cpus>] [-m <MB>] [-d <disk image>] [-p <partition>] \
  [-r|--replicate-injects]                           # -r recreates the disk snapshot and injections
phenix vm set <exp> [vm] [-c|--cpu <n>] [-m|--mem <MB>] [-d|--disk <image>] [-p|--partition <n>] \
  [--do-not-boot] [--snapshot] [-L key=value ...] [--append-labels]
phenix vm net connect <exp> <vm> <iface index> <vlan id>
phenix vm net disconnect <exp> <vm> <iface index>
phenix vm capture start <exp> <vm> <iface index> <output file>
phenix vm capture start-subnet <exp> <subnet>
phenix vm capture stop <exp> <vm>
phenix vm capture stop-subnet <exp> <subnet>
phenix vm capture stop-all <exp>
phenix vm memory-snapshot <exp> <vm> <path>
phenix vm mount <exp> <vm> [host path]               # mount a running VM's filesystem on the headnode
phenix vm unmount <exp> <vm>
```

`phenix vm mount` mounts to `<mount-dir>/<experiment>/<vm>` unless an explicit
host path is given, and prints the resulting mount path; `<mount-dir>` comes
from the `--mount-dir` root flag (`PHENIX_MOUNT_DIR`), defaulting to
`<base-dir.phenix>/mounts`.

### Targeting VMs by label

`info`, `pause`, `resume`, `restart`, `reset-disk`, `redeploy`, `shutdown`,
`kill`, and `set` accept `-l/--label` (repeatable, comma-separated values also
accepted) instead of VM names. Labels are matched case-insensitively against a
VM's label keys, support glob patterns (`*`, `?`, `[...]`), and the special
value `all` selects every VM in the experiment. A label filter matching no VM
is an error (`no VMs matched label(s): ...`).

```bash
phenix vm info my-exp --label role-web
phenix vm shutdown my-exp --label 'site-*'
phenix vm restart my-exp --label all
```

Labels are written with `phenix vm set --label-changes/-L key=value` (repeatable);
`--append-labels` merges them into the VM's existing labels rather than
replacing the set. Labels are the only VM setting that can be changed while an
experiment is running.

```bash
phenix vm set my-exp node1 -L role=web -L site=east --append-labels
```

## `phenix image` — disk image (vmdb2) configuration and builds

```bash
phenix image list
phenix image create <name> [-s 10G] [-v minbase] [-r jammy] [-m <mirror>] [-l comp1,comp2] \
  [-f qcow2] [-c] [-R] [-O overlay1,overlay2] [-P pkg1,pkg2] [-T script1,script2] \
  [-k arg1,arg2] [--skip-default-pkgs] [--no-virtuals]
phenix image create-from <existing> <new>
phenix image edit <name>
phenix image build <name> [-v|-x] [-c|--cache] [--dry-run] [-o <output dir>]
phenix image delete <name>
phenix image append|remove|update <name> ...
phenix image inject-miniexe <path/to/exe> <path/to/disk>
```

Default variant/release/mirror are `minbase`/`jammy`/Ubuntu archive; `-l`
restricts which mirror components packages come from; `-f` supports `qcow2`
(default) and other formats vmdb2 supports. On `build`, `-x` is very verbose
and also writes `<image name>.log`, `-c` caches the rootfs as a tar archive for
faster rebuilds, and `--dry-run` does everything except call vmdb2.

For build scripts, overlays, `Image` config fields, vmdb2 environment, and
build troubleshooting, read the sibling
[`phenix-image`](../../phenix-image/SKILL.md) skill.

## `phenix vlan` — VLAN aliasing/ranges per experiment

```bash
phenix vlan alias <exp> <alias name> <vlan id>   # view (no value) or set an alias
phenix vlan range <exp> <range min> <range max>  # view or set the VLAN pool range
```

## Other subcommands

```bash
phenix mm <minimega args>...     # pass raw commands to (or attach to) minimega
phenix settings list|get|set|unset [key] [value]
phenix settings db ...            # legacy BoltDB-backed settings
phenix ui                         # run the phenix web UI/API server
phenix util app-json <exp>        # print the experiment JSON a user app receives on stdin
phenix util role-table            # print the permissions/roles table
phenix completion bash|zsh|fish|powershell
phenix version
```
