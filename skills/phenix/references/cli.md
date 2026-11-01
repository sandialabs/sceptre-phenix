# phenix CLI Reference

Full subcommand and flag reference. The [skill body](../SKILL.md) covers the
concepts and the commands needed for common workflows.

## Global flags and configuration precedence

Every subcommand accepts these persistent flags (bound to viper, so each also
has a matching config-file key and `PHENIX_*` env var):

| Flag | Config key / Env var | Default | Description |
|---|---|---|---|
| `--store.endpoint` | `store.endpoint` / `PHENIX_STORE_ENDPOINT` | `bolt:///etc/phenix/store.bdb` (root) or `bolt://~/.phenix.bdb` (non-root) | Data store endpoint (`bolt://<path>`) |
| `--base-dir.phenix` | `base-dir.phenix` / `PHENIX_BASE_DIR_PHENIX` | `/phenix` | Base phēnix data directory |
| `--base-dir.minimega` | `base-dir.minimega` / `PHENIX_BASE_DIR_MINIMEGA` | `/tmp/minimega` | Base minimega directory |
| `--base-dir.injects` | `base-dir.injects` / `PHENIX_BASE_DIR_INJECTS` | `<base-dir.phenix>/injects` | Where `phenix workflow apply` stages `phenix-injects/` (as `<base-dir.injects>/<name>`). The server's value wins over the CLI's own unless the flag is given; see the `injects` step in [`workflow.md`](workflow.md). Use an absolute path |
| `--base-dir.topologies` | `base-dir.topologies` / `PHENIX_BASE_DIR_TOPOLOGIES` | `<base-dir.phenix>/topologies` | Where `phenix workflow apply NAME` looks up a bare topology directory name. Use an absolute path |
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
| `--color` | `color` / `PHENIX_COLOR` | `auto` | When to color the text output: `auto`, `on`, or `off` |
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

## `phenix config` — manage stored configs (topology/scenario/experiment/image/user/role)

```bash
phenix config list <kind|all>                      # table of stored configs
phenix config get <kind>/<name> [-o yaml|json] [-p] # dump a config
phenix config create </path/to/file.yaml> ...       # create from file(s) or dir; validates against its apiVersion's schema
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
  [--disabled-apps app1,app2] [--vlan-min N] [--vlan-max N] [-b <bridge>] \
  [--annotation key=value]... [--node-annotation key=value]...
phenix experiment edit <exp> [--force]              # --force overrides the running-experiment check
phenix experiment delete <exp> [-f|--force]          # -f stops a running experiment before deleting it, forcing the stop
phenix experiment schedule <exp> <algorithm>         # e.g. round-robin, isolate-experiment, subnet-compute
phenix experiment start <exp|all> [--dry-run] [--honor-run-periodically] \
  [--treat-mm-errors-as-warnings] [--vlan-min N] [--vlan-max N]
phenix experiment stop <exp|all> [-K] [-f|--force]   # -f marks it stopped even if minimega cannot kill its VMs
phenix experiment restart <exp> [--dry-run]
phenix experiment reconfigure <exp>
phenix experiment trigger <lifecycle> <exp> [app ...]  # re-fire a lifecycle stage for app(s)
phenix experiment scorch <exp> [-r|--run <id>]      # start SCORCH run <id> (default 0) for the experiment
```

`-t`/`-s` accept either the name of an already-stored config or a path to a
YAML/JSON file (in which case it's auto-created as a config first).

`--annotation` (repeatable) adds an experiment metadata annotation; `topology`
and `scenario` are set by phenix and rejected. `--node-annotation`
(repeatable) adds an annotation to every VM in the experiment's own copy of the
topology, leaving the Topology config unchanged; a VM that already has the key
keeps its value. Its value `true` or `false` becomes a boolean and a JSON list
or object keeps that type (`--node-annotation
'phenix/startup-autotunnel=["8080:80"]'`); anything else, numbers included,
stays a string. The annotations the default apps read are in
[`annotations.md`](annotations.md).

`start all` starts every stopped experiment. `--dry-run` (also on `restart`)
does everything except call out to minimega, which isolates config and
scheduling problems from minimega problems. Before minimega gets anything, `start`
checks each booting VM's disk images (following qcow2 backing chains) and
CD-ROM image (`advanced.cdrom`, `.iso` drives: existence only) on the head
node, and refuses with one line per VM and disk when a file is missing or
qemu cannot open an image of the chain (encryption, an unknown qcow2 version
or feature bit, a corrupt image opened read/write); a qcow version 1 or dirty
qcow2 image only warns;
`restart` does the same check before it stops the experiment (warnings only
when the scenario has a user app). `--dry-run` and
`--treat-mm-errors-as-warnings` make the problems warnings; the
`experiment.disk-check` setting (`error` default, `warn`, `off`) applies to
every start path, CLI, REST, UI, workflow and bulk VM start/restart jobs. `--honor-run-periodically` keeps
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
phenix workflow apply <DIR|NAME> --follow-inject-symlinks # follow symlinks in phenix-injects/ and copy as regular files
```

The argument, the directory layout, the steps, the workflow config, and the
troubleshooting table are in [`workflow.md`](workflow.md).

## `phenix job` — follow the running server's background jobs

```bash
phenix job list [--experiment X] [--kind K] [--active] [-o json]  # every user's jobs, newest first
phenix job get <ID> [-o json]       # the job and each item's outcome, error and retryable flag
phenix job cancel <ID>              # queued: canceled at once; running: stops when its work notices
phenix job wait <ID> [--timeout D]  # poll every second; exit 0 succeeded, 2 partial, 1 failed/canceled/timeout
```

Talks to `phenix ui` over `--unix-socket` as Global Admin; without a server it
fails with `phenix ui is not running (no socket at ...)`. Jobs are started only
from the web UI or REST API (the CLI's `vm` and disk commands run in-process and
are not jobs); see [`jobs.md`](jobs.md).

## `phenix vm` — manage running VMs within an experiment

```bash
phenix vm info <exp> [vm] [--host h]                 # table of VM(s); --host: only VMs on that host
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
phenix vm capture stream <exp> <vm> <iface> [-o file] [--from-now]  # running capture as pcap, e.g. | wireshark -k -i -
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

`phenix vm info` also takes `--host` (repeatable, comma-separated values also
accepted) to list only the VMs running on any of the named cluster hosts,
matched exactly as minimega names them. It combines with the VM name or
`--label`, and with `--filter`, by AND, and a host filter matching no VM is an
error (`no VMs matched host(s): ...`).

```bash
phenix vm info my-exp --host compute1 --label role-web
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
phenix image create <name> [-s 10G] [-v minbase] [-r noble] [-m <mirror>] [-l comp1,comp2] \
  [-f qcow2] [-c] [-R] [-O overlay1,overlay2] [-P pkg1,pkg2] [-T script1,script2] \
  [-k arg1,arg2] [--skip-default-pkgs] [--no-virtuals]
phenix image create-from <existing> <new>
phenix image edit <name>
phenix image build <name> [-v|-x] [-c|--cache] [--dry-run] [-o <output dir>]
phenix image delete <name>
phenix image append|remove|update <name> ...
phenix image inject-miniexe <path/to/exe> <path/to/disk>
```

Default variant/release/mirror are `minbase`/`noble`/Ubuntu archive; `-l`
restricts which mirror components packages come from; `-f` supports `qcow2`
(default) and other formats vmdb2 supports. On `build`, `-x` is very verbose
and also writes `<image name>.log`, `-c` caches the rootfs as a tar archive for
faster rebuilds, and `--dry-run` does everything except call vmdb2.

For build scripts, overlays, `Image` config fields, vmdb2 environment, and
build troubleshooting, read the sibling
[`phenix-image`](../../phenix-image/SKILL.md) skill.

## `phenix vlan` — VLAN aliasing/ranges per experiment

```bash
phenix vlan alias [exp]                          # list VLAN aliases (all experiments, or one)
phenix vlan alias <exp> <alias name>             # print the alias's VLAN ID (e.g. `105`)
phenix vlan alias <exp> <alias name> <vlan id>   # set an alias (-f to overwrite)
phenix vlan range <exp> <range min> <range max>  # view or set the VLAN pool range
```

## Other subcommands

```bash
phenix mm <minimega args>...     # pass raw commands to (or attach to) minimega
phenix settings list|get|set|unset [key] [value]
phenix settings db ...            # legacy BoltDB-backed settings
phenix ui                         # run the phenix web UI/API server
phenix healthcheck [--ready] [--url URL] [-o json]  # probe the UI server's /healthz (or /readyz); exit 0 on 200
phenix doctor [-o json] [--timeout 3s] [--skip ID|GROUP,...] [--strict] [-x] [-p] [--report]  # offline; prints a header (phenix/minimega versions, OS/kernel, uid/gid, cwd, config file, container), the settings with source (secrets redacted; -x omits), then checks: config syntax/keys, each set PHENIX_*/PATH/EDITOR/SUDO_* variable (parsed as phenix does; unknown PHENIX_* and ones overriding a different config file value warn), proxy-env (env group: HTTP(S)_PROXY in either case set and a set NO_PROXY/no_proxy not covering localhost, 127.0.0.1, ::1, the host name and the ui.listen-endpoint host warns; forms *, .domain, *.domain, CIDR, host:port as Go reads them; names variables, never values), the settings every command reads (bridge-mode, deploy-mode, experiment.disk-check, color, log.level, log.system.*), store/log/base dirs, phenix ui startup (auth: JWT key incl. dev|<user>|<role> fields, jwt-lifetime under 1m, ui.users entries parsed as phenix ui does and roles vs built-in roles, default admin user, proxy-jwt on a wildcard listen address, ignored proxy header; tls: half-set or unloadable key pair; ui.tls-cert (only with both set): cert expired/not yet valid fails, expiring within 30 days, no DNS/IP SANs, or SANs not covering the ui.listen-endpoint host (else os.Hostname; a short name matches its FQDN SAN) warn, names redacted by --report; listen and file-server endpoints; socket: unix-socket directory writable, only a warning when a socket is already there; unix-socket-gid: the user is root or in the group, named or not, and as root a gid with no group warns), server (GET /api/v1/options on unix-socket as a client only, never creating or removing it: no socket/stale socket/non-socket file/timeout warn, EACCES fails; X-Phenix-Version differing from this build and a bridge-mode or deploy-mode set here and differing from phenix ui's warn, naming the mode commands use), stuck-status (server group, warn only, store read-only: app running flags (appRunningStageStatus, SCORCH too, with its runID) and delayed starts of a running experiment with no phenix ui answering and no other phenix process in /proc naming the experiment warn, and an interrupted start warns; phenix ui clears the flags and resumes delayed starts as it starts; starting/stopping live only in phenix ui memory and jobs do not cover starts, app stages or SCORCH runs), ui-limits (server group, Linux only: phenix ui pid from SO_PEERCRED on unix-socket, else /proc cmdline phenix ui; more than 80% of its soft open-files limit open in /proc/<pid>/fd warns; unreadable /proc/<pid> or no phenix ui on this host skips), minimega (version; a failed dial names no socket, stale socket or wrong user), minimega-env (global args: -filepath must exist and be readable for phenix, warns when not writable; -base vs base-dir.minimega; degree, abssnapshot and mesh size), tools (qemu-img and vmdb2 missing fail, filebeat missing warns, disk warns at 90% and fails at 98% full; Linux-only host checks of the head node: kernel-modules (no /lib/modules/<uname -r> or /var/run/reboot-required warns; a container without /lib/modules skips), nbd (minimega disk inject for injects and inject-miniexe: no qemu-nbd fails; nbd not in lsmod, built in, max_part missing or 0, no free /dev/nbdN (no /var/lock/qemu-nbd-nbdN and no /sys/block/nbdN/pid), no /dev/nbd* in a container, and devices a failed inject can leave (lock file without pid, dead pid, a qemu-nbd with its image and mount) warn), openvswitch (no ovs-vsctl fails, or skips in a container; ovs-vsctl --timeout=<s> show failing or timing out fails, permission denied warns; ovs-vswitchd not running and no openvswitch module warn; reports the OVS version and whether bridge phenix exists), image-build (rows tools, root, loop, keyrings: missing debootstrap/kpartx/losetup/qemu-img, no CAP_SYS_ADMIN, no free loop device and no /dev/loop-control, and a missing debootstrap script or keyring for a stored image config's release warn; the keyrings row opens the store read-only with bbolt, waiting at most --timeout for the lock, and skips when locked)), leftovers (images group, rows snapshots, files, mounts, taps, mirrors; all warn only: <head node>_<exp>_<vm>_snapshot files in the images dir of experiments not in the store, <base-dir.phenix>/experiments/<name> and <images>/<name> (with files/ or miniccc_responses/) dirs of deleted experiments with sizes, 9p mounts under mount-dir of deleted experiments or VMs minimega does not run, mega_tap* devices in /sys/class/net and OVS ports with no VM or host tap in any minimega namespace (namespace <ns> vm info/tap/capture) or no network device, mirror-<tap> OVS mirrors with no capture; what belongs to a namespace with a running minimega VM is kept; in a container, taps are checked only when /sys/class/net shows a mega_tap, an OVS bridge or ovs-system; a part without the store (also no store file), minimega or ovs-vsctl skips with the reason), disk-space (images group: the filesystems of the images dir, minimega's -base and -logfile, once each; free < max(10%, 5 GiB) warns, naming the minimega log size and the size leftovers found; an inject dirs row warns about dstImg* dirs in -base and says which are mounted), features (tunneler-download and webshark say whether phenix ui turns them on, relative to the working directory; ui.features warns on an unknown name with a did-you-mean and on tunneler-download/file-server set without what they need, fails on webshark without a build, which phenix ui refuses; ui.logs.minimega-path missing, or unset while minimega logs to a file, warns), apps group (apps lists phenix-app-*/phenix-scorch-component-* on PATH; scenario-apps: an app or SCORCH component type (type, else name) of a stored scenario or experiment that is not built in (apps ntp, serial, startup, vrouter, tap, soh, scorch; components break, pause, soh, tap) and not on PATH warns), and stored-config checks, all warn only and skip without the store: scenario-hosts (config: a scenario app hosts[].hostname that is not a node of the experiment's topology), inject-sources (files: an injection src that does not exist; experiments only for VMs with snapshot and an inject partition other than 0, relative src under baseDir, absolute src under baseDir not checked since apps write it at the start; topologies only absolute src), image-configs (tools: an overlay dir that does not exist; script files not on disk only noted, since the config holds their copies), rbac-users (auth, only with ui.jwt-signing-key set: a user rbac.roleName that is not a stored or built-in role, and literal resourceNames (no glob or !) in experiment/vm or user policies naming configs not in the store; never prints passwords or tokens); the store-reading checks open bbolt read-only once per kind per run, waiting at most --timeout for the lock, and after one lock timeout the others do not wait; then a summary with a hint per failure; --color colors the statuses, the summary (worst status) and the hints, never with -o json or --report. --skip takes check ids or groups (config, env, settings, files, dirs, ui, auth, tls, socket, server, minimega, tools, images, apps, features); JSON has schemaVersion 1 and a stable id per check; --report fences the output for a bug report and replaces host names, IP addresses, user names and emails with consistent tokens (<host-1>, <ip-1>, <user-1>, <email-1>; paths keep their shape) and passwords, the JWT key, secret-like key=value values and URL credentials with <redacted>, keeping localhost, 127.0.0.1, ::1, 0.0.0.0, ::, the users root/phenix/minimega/nobody/admin@foo.com and a hostname-suffixes suffix (ws42-phenix -> <host-1>-phenix); it never prints the hostname (also in <host>_<exp>_<vm>_snapshot names, the host minimega answers from, the ui.listen-endpoint host and the TLS certificate DNS names); -p (--problems-only) shows only warn and fail rows (JSON: checks filtered, hiddenChecks count, status from all checks), the summary still counts every check and says how many are hidden, and the exit code does not change; exit 1 on a failed check, or a warning with --strict
phenix util app-json <exp>        # print the experiment JSON a user app receives on stdin
phenix util role-table            # print the permissions/roles table
phenix completion bash|zsh|fish|powershell
phenix version
```

`phenix mm` is also available as `phenix minimega`. Its flags (`-n/--namespace`
to scope commands to a minimega namespace, `-a/--attach` to open the
interactive console) and any global flags must come before the first minimega
argument; everything after it is passed to minimega unchanged, so
`phenix mm -n my-exp cc exec ls -a` sends `-a` to `ls`. Arguments are quoted
for minimega as needed; a single argument is sent as-is, so
`phenix mm -n my-exp "vm info"` passes a whole minimega command line. The
command exits non-zero, and logs the error, when minimega reports an error or
the connection to minimega is lost, so scripts can check its exit status.
