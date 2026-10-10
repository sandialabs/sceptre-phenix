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
| `--base-dir.injects` | `base-dir.injects` / `PHENIX_BASE_DIR_INJECTS` | `<base-dir.phenix>/injects` | Where `phenix workflow apply` stages `phenix-injects/` (as `<base-dir.injects>/<name>`). The server's value wins over the CLI's own unless the flag is given; see the `injects` step in [`workflow.md`](workflow.md). Use an absolute path |
| `--base-dir.topologies` | `base-dir.topologies` / `PHENIX_BASE_DIR_TOPOLOGIES` | `<base-dir.phenix>/topologies` | Where `phenix workflow apply NAME` looks up a bare topology directory name. Use an absolute path |
| `--base-dir.builder-templates` | `base-dir.builder-templates` / `PHENIX_BASE_DIR_BUILDER_TEMPLATES` | `<base-dir.phenix>/builder/templates` | Template files `phenix ui` reads at start as read-only Builder server collections (see [`builder/templates.md`](builder/templates.md#server-collections)). Use an absolute path |
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
| `--unix-socket` | `unix-socket` / `PHENIX_UNIX_SOCKET` | `/tmp/phenix.sock` | Unix socket to listen on (`ui`) or connect to (other commands, to inherit server-set options; `phenix workflow apply` sends all its requests through it) |

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

The Builder, the web topology editor at `/builder`, has no CLI equivalent
for sharing, Import, publishing a draft, legacy diagram conversion or
managing the icon library. Its CLI commands are
[`phenix builder publish`](#phenix-builder--builder-documents-drafts-and-node-templates)
(a topology from a Builder document file, in the store) and `phenix builder
drafts` and `phenix builder templates` (drafts and Node Templates on a
running server, through its REST API). See [`builder.md`](builder.md).

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

A config written as YAML (`phenix config get -o yaml`, `phenix config edit`,
`GET /configs/{kind}/{name}` with `Accept: application/x-yaml`, and
`POST /configs/download`) loads as the stored config: a string yaml.v3 could
not read back (one starting with a line break, or whose first line starts with
a tab) is written double quoted.

Annotations are text, except `builder-doc` on a Topology, which every JSON
and YAML form of a config shows as a map of `digest`, `id` and `path` (see
[`builder/published-documents.md`](builder/published-documents.md#the-builder-doc-reference)). A Topology
whose `builder-doc` is not valid is refused on create and update, also with
`--skip-validation`. `config edit` can change an annotation but not remove
one (annotation maps merge).

`config create` takes files and directories (walked recursively). A Builder
document (a Builder JSON or YAML download) is not a config: one found in
a directory is skipped with the log line `skipped Builder document; use
phenix builder publish`, and one named on the command line is refused with
`<file> is a Builder document, not a configuration: use "phenix builder
publish <file>" to create its topology`. A template file or a package is
not a config either: skipped in a directory with a debug log line, refused
on the command line (`<file> is a Builder template file, not a
configuration: ...`, `<file> is a Builder package, not a configuration:
...`).

## `phenix builder` — Builder documents, drafts and Node Templates

`publish` works on the store; `drafts` and `templates` call the REST API of
a running server (see [below](#drafts-and-templates-through-the-rest-api)).

```bash
phenix builder publish </path/to/document> [-n|--name <topology>] [--update] [--dry-run] \
  [--user <user>] [--record-path]
```

`publish` reads one Builder document file (Builder
JSON or Builder YAML, decided by content; at most 5 MiB; no `${NAME}`
expansion), checks it as Builder's Publish does, and stores the Topology
config it describes, with the document stored as the topology's published
diagram and named in its `builder-doc` annotation (`digest` and `id`). It
writes to the store from the CLI process: it needs no running `phenix ui`.
Topology only: no scenario, no experiment, no VLAN aliases (each is a warning
when the document has one).

| Flag | Meaning |
|---|---|
| `-n`, `--name` | Topology name. Default: the document's name as the Publish dialog proposes it (`Pump station` gives `Pump-station`). Must be a config name. |
| `--update` | Replace an existing topology of that name. Allowed only when the topology is unchanged since its stored document was published, or the document was imported from the topology as it is now. A `builder-xml` (legacy Builder) topology is replaced only in the second case; the update deletes `builder-xml` and warns `The legacy Builder diagram of topology X was replaced by this diagram.` No force flag. |
| `--dry-run` | Run every check and print a report on stdout (document, file, digest, document ID, what would happen, node count, warnings). Writes nothing. |
| `--user` | User to record as the publisher of the stored document. Default: the sudo caller, else the OS account. |
| `--record-path` | Also write the file's absolute path as `builder-doc.path`. The file name must end in `.json`, `.yaml` or `.yml`. A path the server would not read (outside `base-dir.phenix`, or below the mount directory) is a warning, not an error. |

```bash
phenix builder publish pump-station.builder.json              # creates Topology Pump-station
phenix builder publish pump-station.builder.json              # again: "topology already up to date", exit 0
phenix builder publish pump-station.builder.yaml -n pump-lab  # another name
phenix builder publish pump-station.builder.json --update     # after the file changed
phenix builder publish pump-station.builder.json --dry-run    # check only; prints the digest
```

Exit status is 0 when the topology was created, updated or already up to
date, and 1 otherwise. Success and warnings are log lines on stderr
(`topology created`, `topology updated`, `topology already up to date`, with
`name`, `document` and `digest`); only `--dry-run` writes to stdout. A
document that cannot be published is an error that lists every blocker on
its own line. Common refusals: `topology X already exists; use --update to
replace it`; `topology X was changed after it was published, and replacing
it would discard that change`; `<file> is not a valid Builder document:
...`.

A running `phenix ui` on the same store sees the topology after a page
reload; nothing serializes a CLI publish with a UI publish of the same
topology. The Configs page opens the published topology in the Builder, not
as text (it has `builder-doc`); `phenix config edit` edits it as text.
Details and the full rules are in
[`builder/cli.md`](builder/cli.md#cli-phenix-builder-publish).

### Drafts and templates through the REST API

```bash
phenix builder drafts list [--shared] [--owner <user>] [-o table|json|yaml]
phenix builder drafts export <owner>/<draft> [--format json|yaml] [--output <file>] \
  [--package [--include scenarios,topologies,icons,images]]
phenix builder drafts validate <owner>/<draft> [-o table|json|yaml]
phenix builder drafts preflight <owner>/<draft> [--check capacity,network,disks,apps] \
  [--experiment <name>] [--strict] [-o table|json|yaml]
phenix builder templates list [--owner <user>] [-o table|json|yaml]
phenix builder templates export [--collection <name|id>] [--owner <user>] [--format yaml|json] [--output <file>]
phenix builder templates import <file> [--name <collection>]
```

`--url` (`PHENIX_URL`) names the server and `--token` (`PHENIX_TOKEN`) the
API token sent as `X-Phenix-Auth-Token: Bearer <token>`: the requests have
the token user's permissions; a server with auth off needs none. A token
sent to an `http` URL of a host other than localhost or a loopback address
is a warning on stderr, not a refusal. Without `--url` they go to the unix
socket (`--unix-socket`, default `/tmp/phenix.sock`) as global-admin, after
checking it is a socket owned by the caller or root and not writable by
others (else exit 2); a token without a URL is refused, and `drafts list`
lists every draft without `--shared`. A flag given an empty value counts as
not given. Redirects are refused, not followed. `drafts export --package`
writes the package alone and prints the server's warnings on stderr. `-o`
defaults to `table`. Exit status: 0 success, 1 findings
(validate errors, a failed preflight check, or with `--strict` an
unavailable one), 2 refused or no answer (connection, redirect, 401/403,
404, unknown subcommand, invalid arguments or file). The report
is written before exit 1. JSON shapes and the rules are in
[`builder/cli.md`](builder/cli.md#cli-phenix-builder-drafts-and-templates).

```bash
phenix builder drafts validate alice/riverside --url https://phenix.example --token "$PHENIX_TOKEN" -o json
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

## `phenix workflow` — deploy a topology directory through the running server

```bash
phenix workflow apply <DIR|NAME>                    # stage injects, upsert configs, apply the workflow config
phenix workflow apply <DIR|NAME> -n                 # --dry-run: validate everything, log the server's plan, change nothing
phenix workflow apply <DIR|NAME> -f                 # --force: allow restarting a running experiment
phenix workflow apply <DIR|NAME> -b <name>          # --branch-name: workflow branch name (default: the directory's name)
phenix workflow apply <DIR|NAME> -c <file>          # --config: workflow config, relative to DIR unless absolute
```

The argument, the directory layout, the steps, the workflow config, and the
troubleshooting table are in [`workflow.md`](workflow.md).

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
