# phenix CLI Reference

What each subcommand is for, with the command lines common workflows need. The
[skill body](../SKILL.md) covers the concepts.

The generated CLI reference owns every command's flags and their defaults:
[phenix.sceptre.dev/latest/cli](https://phenix.sceptre.dev/latest/cli/), built
from `docs/content/cli/` in the phēnix repository, one page per top-level
command. `make cli-docs` regenerates it from the Cobra command tree, and CI
fails when it is out of date. The command lines below are a summary; read the
generated page for a command's complete flags.

## Global flags and configuration precedence

The options of `phenix` itself, listed on the reference's
[overview page](https://phenix.sceptre.dev/latest/cli/#options), are
persistent flags every subcommand accepts. Each is bound to viper: its
config-file key is the flag name, and its environment variable is `PHENIX_`
followed by the name in upper case with `.` and `-` as `_`, so
`--log.system.max-size` is `log.system.max-size` and
`PHENIX_LOG_SYSTEM_MAX_SIZE`. The defaults of `--store.endpoint` and
`--log.system.path` depend on the user: `bolt:///etc/phenix/store.bdb` and
`/var/log/phenix/phenix.log` as root, `bolt://$HOME/.phenix.bdb` and
`$HOME/phenix_logs/phenix.log` otherwise.

`--store.endpoint` takes a BoltDB path, `bolt://<path>`, the only data store.

`--bridge-mode` defaults to `manual`, which uses the bridge name given when an
experiment is created, or `phenix`; `auto` names each experiment's bridge
after the experiment. A command that reaches a `phenix ui` running in `auto`
mode over `--unix-socket` uses `auto` whatever its own setting. See
[Bridge Mode](https://phenix.sceptre.dev/latest/bridge-mode/).

`--color` (`auto` default, `on`, `off`) sets when phenix colors its
text output: doctor statuses, the summary and hints, the `Error:` label,
console log levels and warning or error messages, workflow apply actions,
the status columns of `experiment list`, `vm info` and `job`. `auto` colors
only a terminal, and not when `NO_COLOR` is set or `TERM` is `dumb`. JSON,
YAML and `doctor --report` never have color. phenix refuses another value.

Precedence (highest to lowest): **1. command-line flag** → **2. environment
variable** (`PHENIX_*`; empty is ignored) → **3. `config.yaml`**, with `users.yaml`
merged over it (managed with `phenix settings set`/`unset`, which change the file
phenix read; `log.*`, `ui.logs.level`, `ui.users` and `experiment.disk-check` are hot-reloaded, other keys
need a `phenix ui` restart) → **4. built-in default**. A `PHENIX_*` variable hides
what `settings set` writes; `phenix doctor` warns about each such override. See
[Settings & Configuration](https://phenix.sceptre.dev/latest/settings/) for the
full settings reference, including UI-only settings (`ui.logs.level`,
`ui.features`, `ui.file-server-endpoint`, `ui.shutdown-grace-period`,
`ui.netflow.max-size`) not
exposed as root-level CLI flags.

`phenix ui --features vm-mount` (equivalently `ui.features: vm-mount` in
`config.yaml`, or `PHENIX_UI_FEATURES=vm-mount`) enables the optional
"VM mount" UI feature, which lets users transfer files to and from a running
VM's filesystem directly from the web UI (backed by the `/experiments/{exp}/vms/{name}/mount`,
`/unmount`, `/files`, `/files/download`, `/files/upload` API routes). It's
disabled by default and requires restarting `phenix ui` to take effect. The CLI
equivalents (`phenix vm mount`/`unmount`) are always available.

`phenix ui` stops cleanly on `SIGINT`/`SIGTERM` within
`--shutdown-grace-period` (`ui.shutdown-grace-period`,
`PHENIX_UI_SHUTDOWN_GRACE_PERIOD`; whole seconds, default `5`, `0` waits for
nothing, a negative or non-integer value such as `5s` is refused). On shutdown
it:

- stops listening and lets requests in progress finish;
- closes every websocket with code `1001`;
- answers new websockets, consoles and jobs with `503` and `Retry-After`;
- gives running jobs half the period, then cancels them;
- ends minimega consoles and SCORCH terminals;
- logs `shutdown complete` (or a warning naming what was still running) and
  exits 0.

A second signal exits at once with 130 (`SIGINT`) or 143 (`SIGTERM`). A set
`ui.file-server-endpoint` that cannot listen makes `phenix ui` exit with an
error.

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

Stored configs carry `metadata.revision`, set by the store on every write and
ignored on input. `config edit` refuses to save if the config was written
while the editor was open ("changed while you edited it"); run it again
to edit the newer version.

## `phenix experiment` — experiment lifecycle

```bash
phenix experiment list
phenix experiment apps                              # list available apps
phenix experiment schedulers                        # list scheduling algorithms
phenix experiment create <exp> -t <topology> [-s <scenario>] [flags]
phenix experiment edit <exp> [--force]              # --force overrides the running-experiment check
phenix experiment delete <exp> [-f|--force]          # -f stops a running experiment before deleting it, forcing the stop
phenix experiment schedule <exp> <algorithm>         # e.g. round-robin, isolate-experiment, subnet-compute
phenix experiment start <exp|all> [--dry-run] [flags]
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
phenix vm redeploy <exp> [vm] [flags]                # new CPU, memory or disk; -r recreates the disk snapshot and injections
phenix vm set <exp> [vm] [flags]                     # change VM settings and labels
phenix vm net connect <exp> <vm> <iface index> <vlan id>
phenix vm net disconnect <exp> <vm> <iface index>
phenix vm capture start <exp> <vm> <iface index> <output file>
phenix vm capture start-subnet <exp> <subnet>
phenix vm capture stop <exp> <vm> [iface]           # iface name or index stops only that one
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
`--label`, and with `--filter`, by AND, as the UI's Hosts and Labels filters do, and a
host filter matching no VM is an error (`no VMs matched host(s): ...`).

```bash
phenix vm info my-exp --host compute1 --label role-web
```

Labels are written with `phenix vm set --label-changes/-L key=value` (repeatable);
`--append-labels` merges them into the VM's existing labels rather than
replacing the set. Labels are the only VM setting that can be changed while an
experiment is running. A label holding `"`, `\`, `$`, a control character other
than a newline or tab, or starting with `file:`, `http:`, `https:` or `tar:` is
rejected, as minimega would read it as something else. With `--append-labels`,
the VM's existing labels must follow the same rules.

```bash
phenix vm set my-exp node1 -L role=web -L site=east --append-labels
```

## `phenix image` — disk image (vmdb2) configuration and builds

```bash
phenix image list
phenix image create <name> [flags]
phenix image create-from <existing> <new>
phenix image edit <name>
phenix image build <name> [flags]
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
phenix settings list|get|set|unset [key] [value]  # phenix ui applies log.*, ui.logs.level, users, experiment.disk-check at once; others on restart (it logs which)
phenix settings db ...            # legacy BoltDB-backed settings
phenix ui                         # run the phenix web UI/API server
phenix healthcheck [--ready] [--url URL] [-o json]  # probe the UI server's /healthz (or /readyz); exit 0 on 200
phenix doctor [-o json] [--timeout 3s] [--skip ID|GROUP,...] [--strict] [-x] [-p] [--report]  # offline; prints a header (phenix/minimega versions, OS/kernel, uid/gid, cwd, config file, container), the settings with source (secrets redacted; -x omits), then checks: config syntax/keys, config-paths (every config.*/users.* in ., ~/.config/phenix unless root, /etc/phenix; phenix reads the first of each name, and another found warns), settings-file (the file settings set writes; warns when it is not the file read, not YAML or not writable), each set PHENIX_*/PATH/EDITOR/SUDO_* variable (parsed as phenix does; unknown PHENIX_* and ones overriding a different config file value warn), proxy-env (env group: HTTP(S)_PROXY in either case set and a set NO_PROXY/no_proxy not covering localhost, 127.0.0.1, ::1, the host name and the ui.listen-endpoint host warns; forms *, .domain, *.domain, CIDR, host:port as Go reads them; names variables, never values), the settings every command reads (bridge-mode, deploy-mode, experiment.disk-check, color, log.level, log.system.*), store/log/base dirs, store-file (reads only the first two pages, never bbolt.Open; not BoltDB, another version or byte order, truncated, or both meta pages corrupt fail; one corrupt meta page warns), store-owner (another owner warns unless root; as root under sudo phenix, an owner other than SUDO_USER warns), store-root (a per-user default store while /etc/phenix/store.bdb exists warns), phenix ui startup (auth: JWT key incl. dev|<user>|<role> fields, jwt-lifetime under 1m, ui.users entries parsed as phenix ui does and roles vs built-in roles, default admin user, proxy-jwt on a wildcard listen address, ignored proxy header; tls: half-set or unloadable key pair; ui.tls-cert (only with both set): cert expired/not yet valid fails, expiring within 30 days, no DNS/IP SANs, or SANs not covering the ui.listen-endpoint host (else os.Hostname; a short name matches its FQDN SAN) warn, names redacted by --report; listen and file-server endpoints; socket: unix-socket directory writable, only a warning when a socket is already there; unix-socket-gid: the user is root or in the group, named or not, and as root a gid with no group warns), server (GET /api/v1/options on unix-socket as a client only, never creating or removing it: no socket/stale socket/non-socket file/timeout warn, EACCES fails; X-Phenix-Version differing from this build and a bridge-mode or deploy-mode set here and differing from phenix ui's warn, naming the mode commands use), stuck-status (server group, warn only, store read-only: app running flags (appRunningStageStatus, SCORCH too, with its runID) and delayed starts of a running experiment with no phenix ui answering and no other phenix process in /proc naming the experiment warn, and an interrupted start warns; phenix ui clears the flags and resumes delayed starts as it starts; starting/stopping live only in phenix ui memory and jobs do not cover starts, app stages or SCORCH runs), ui-limits (server group, Linux only: phenix ui pid from SO_PEERCRED on unix-socket, else /proc cmdline phenix ui; more than 80% of its soft open-files limit open in /proc/<pid>/fd warns; unreadable /proc/<pid> or no phenix ui on this host skips), minimega (version; a failed dial names no socket, stale socket or wrong user), minimega-env (global args: -filepath must exist and be readable for phenix, warns when not writable; -base vs base-dir.minimega; degree, abssnapshot and mesh size), minimega-limits (Linux only, rows open files and memory, warn only: minimega's pid from <-base>/minimega.pid; its /proc/<pid>/limits soft open-files limit below 64 + 5 per VM on this node (QMP, VNC, cc, two qemu pipes) or more than 80% of it open in /proc/<pid>/fd; MemAvailable under 10% of MemTotal, swap over 50% used, or the vm info memory of the VMs on this node over MemTotal + SwapTotal; an unreadable /proc/<pid> or a minimega in another container skips), state-drift (rows experiments and namespaces: stored running experiments with no minimega namespace or no VMs, and namespaces with VMs that no stored experiment owns, warn), vlans (rows pinned and free: two stored experiments pinning one VLAN ID warn; a stopped experiment whose range (vlans.min/max, else minimega's) has too few free VLANs (namespace <ns> vlans, vlans range, vlans blacklist), whose range overlaps another namespace's, or whose pinned VLAN another namespace uses warns), bridge-trunk (Linux only: in a mesh of more than one node (mesh list all), an experiment bridge (phenix, or one with VM taps) with no untagged physical NIC, bond, VLAN device or tunnel port warns; one node skips with "one node, no trunk needed"; use-gre-mesh passes), schedule-hosts (spec.schedules naming a node not in mesh list all, without hostname-suffixes, warn), cc-clients (a RUNNING VM of a running experiment, up 5 minutes or more, that needs miniccc (startup via C2, a delay.c2 target, or the soh app) with no uuid in namespace <ns> cc clients warns; so does a "mismatched miniccc version" line for a current client, logged after its VM launched, in the last MiB of minimega's -logfile; a relative -logfile is not read), tools (qemu-img and vmdb2 missing fail, filebeat missing warns, disk warns at 90% and fails at 98% full; Linux-only host checks of the head node: kernel-modules (no /lib/modules/<uname -r> or /var/run/reboot-required warns; a container without /lib/modules skips), nbd (minimega disk inject for injects and inject-miniexe: no qemu-nbd fails; nbd not in lsmod, built in, max_part missing or 0, no free /dev/nbdN (no /var/lock/qemu-nbd-nbdN and no /sys/block/nbdN/pid), no /dev/nbd* in a container, and devices a failed inject can leave (lock file without pid, dead pid, a qemu-nbd with its image and mount) warn), openvswitch (no ovs-vsctl fails, or skips in a container; ovs-vsctl --timeout=<s> show failing or timing out fails, permission denied warns; ovs-vswitchd not running and no openvswitch module warn; reports the OVS version and whether bridge phenix exists), image-build (rows tools, root, loop, keyrings: missing debootstrap/kpartx/losetup/qemu-img, no CAP_SYS_ADMIN, no free loop device and no /dev/loop-control, and a missing debootstrap script or keyring for a stored image config's release warn; the keyrings row opens the store read-only with bbolt, waiting at most --timeout for the lock, and skips when locked)), disk-chains (images group: reads only the headers of the images in minimega's -filepath, or <base-dir.phenix>/images without one, within --timeout; a missing backing file, a loop or a chain over 64 deep warns, naming at most 5 images; an unlistable dir or running out of time warns; a missing dir gives a skip row), disk-formats (images group, from the same header scan as disk-chains: qcow2 version 2, qcow version 1, vmdk, qed, and images qemu refuses (unknown version or feature bits, AES or LUKS encryption, a corrupt mark) warn, naming at most 5; a second row warns when the local qemu-img is older than a feature needs: zstd 5.1, extended L2 5.2, external data file 4.0; never fails), writable-images (images group: a VM with snapshot: false writes to its drive images; warns when such an image backs another image in the images dir (from the disk-chains scan) or when more than one VM uses it), leftovers (images group, rows snapshots, files, mounts, taps, mirrors; all warn only: <head node>_<exp>_<vm>_snapshot files in the images dir of experiments not in the store, <base-dir.phenix>/experiments/<name> and <images>/<name> (with files/ or miniccc_responses/) dirs of deleted experiments with sizes, 9p mounts under mount-dir of deleted experiments or VMs minimega does not run, mega_tap* devices in /sys/class/net and OVS ports with no VM or host tap in any minimega namespace (namespace <ns> vm info/tap/capture) or no network device, mirror-<tap> OVS mirrors with no capture; what belongs to a namespace with a running minimega VM is kept; in a container, taps are checked only when /sys/class/net shows a mega_tap, an OVS bridge or ovs-system; a part without the store (also no store file), minimega or ovs-vsctl skips with the reason), disk-space (images group: the filesystems of the images dir, minimega's -base and -logfile, once each; free < max(10%, 5 GiB) warns, naming the minimega log size and the size leftovers found; an inject dirs row warns about dstImg* dirs in -base and says which are mounted), image-locks (images group, Linux only: qemu-system/qemu-nbd/qemu-img processes in /proc/<pid>/fd that hold an image or backing file of a stopped experiment and are not VMs of a running one warn: any open of a snapshot: false image, a write open of the others; skips in a container that shows no minimega process; writable-images, leftovers and image-locks share one read-only bbolt open of the store per run, waiting at most --timeout for the lock), features (tunneler-download and webshark say whether phenix ui turns them on, relative to the working directory; ui.features warns on an unknown name with a did-you-mean and on tunneler-download/file-server set without what they need, fails on webshark without a build, which phenix ui refuses; ui.logs.minimega-path missing, or unset while minimega logs to a file, warns), apps group (apps lists phenix-app-*/phenix-scorch-component-* on PATH; scenario-apps: an app or SCORCH component type (type, else name) of a stored scenario or experiment that is not built in (apps ntp, serial, startup, vrouter, tap, soh, scorch; components break, pause, soh, tap) and not on PATH warns; app-runtimes: such a program that this user cannot run, a #! interpreter that does not exist or cannot run, an env program not on PATH, or a #! line with a carriage return warns), and stored-config checks, all warn only and skip without the store: scenario-hosts (config: a scenario app hosts[].hostname that is not a node of the experiment's topology), inject-sources (files: an injection src that does not exist; experiments only for VMs with snapshot and an inject partition other than 0, relative src under baseDir, absolute src under baseDir not checked since apps write it at the start; topologies only absolute src), image-configs (tools: an overlay dir that does not exist; script files not on disk only noted, since the config holds their copies), rbac-users (auth, only with ui.jwt-signing-key set: a user rbac.roleName that is not a stored or built-in role, and literal resourceNames (no glob or !) in experiment/vm or user policies naming configs not in the store; never prints passwords or tokens), store-fs (files: the store dir on NFS/SMB/CIFS/Ceph/9p/Lustre/GPFS/GFS2/AFS or FUSE warns; a store over 100 MiB with over 50% free pages, read from the meta page and the free list header without the lock, warns with bbolt compact; Linux only for the filesystem type), secret-files (files: the TLS key, config/users files that set ui.jwt-signing-key or ui.users, and the store file readable by group or others (mode & 044) warn, and the key or config files owned by another user than root or the phenix user warn; stat only, never the contents); the store-reading checks open bbolt once per kind per run, and after one lock timeout the others do not wait; then a summary with a hint per failure; --color colors the statuses, the summary (worst status) and the hints, never with -o json or --report. --skip takes check ids or groups (config, env, settings, files, dirs, ui, auth, tls, socket, server, minimega, tools, images, apps, features); JSON has schemaVersion 1 and a stable id per check; --report fences the output for a bug report and replaces host names, IP addresses, user names and emails with consistent tokens (<host-1>, <ip-1>, <user-1>, <email-1>; paths keep their shape) and passwords, the JWT key, secret-like key=value values and URL credentials with <redacted>, keeping localhost, 127.0.0.1, ::1, 0.0.0.0, ::, the users root/phenix/minimega/nobody/admin@foo.com and a hostname-suffixes suffix (ws42-phenix -> <host-1>-phenix); it never prints the hostname (also in <host>_<exp>_<vm>_snapshot names, the host minimega answers from, the nodes of its mesh, the nodes in spec.schedules, the ui.listen-endpoint host and the TLS certificate DNS names); -p (--problems-only) shows only warn and fail rows (JSON: checks filtered, hiddenChecks count, status from all checks), the summary still counts every check and says how many are hidden, and the exit code does not change; exit 1 on a failed check, or a warning with --strict
phenix util app-json <exp>        # print the experiment JSON a user app receives on stdin
phenix util role-table            # print every permission phenix checks, and which roles allow it
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
