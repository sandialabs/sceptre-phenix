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
mode over `--unix-socket` uses `auto` whatever its own setting. In `auto`
mode, a stop deletes the experiment's own bridge (named after the experiment,
never `phenix`) on each experiment host, unless another running experiment
uses it; `manual` mode never deletes a bridge. See
[Bridge Mode](https://phenix.sceptre.dev/latest/bridge-mode/).

`--color` (`auto` default, `on`, `off`) sets when phenix colors its
text output: doctor statuses, the summary and hints, the `Error:` label,
console log levels and warning or error messages, workflow apply actions,
the status columns of `experiment list`, `vm info` and `job`. `auto` colors
only a terminal, and not when `NO_COLOR` is set or `TERM` is `dumb`. JSON,
YAML and `doctor --report` never have color. phenix refuses another value.

Precedence (highest to lowest): **1. command-line flag** → **2. environment
variable** (`PHENIX_*`; empty is ignored) → **3. `config.yaml`**, with `users.yaml`
merged over it (managed with `phenix settings set`/`unset`, which change only the key
in the file phenix read and keep its comments; `set` refuses a value that does not have
the type of the setting's flag, and a string setting keeps text such as `0123`; `log.*`, `ui.logs.level`, `ui.users` and `experiment.disk-check` are hot-reloaded, other keys
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

The deprecated `unix-socket-gid` key (`PHENIX_UNIX_SOCKET_GID`) still works
when `ui.unix-socket-gid` is not set, and `phenix ui` then writes a
deprecation warning to stderr. The flag `phenix ui --unix-socket-gid` sets
`ui.unix-socket-gid`. Every deprecated part of the CLI is in
[Deprecations](https://phenix.sceptre.dev/latest/deprecations/).

## Output formats, dry runs and output files

These rules apply to every command. The generated reference shows which
commands have each flag.

- `-o/--output` on list, info, get and status commands takes `table`,
  `json` or `yaml`. Some commands print `text` by default (`version`,
  `experiment apps`, `experiment schedulers`, `experiment files checksum`,
  `experiment netflow status`, `experiment scorch --status`), and
  `vlan list` also takes `csv`. `doctor` and `healthcheck` take `text` or
  `json`. `phenix job` commands take `table`, `json` or `yaml`. JSON and YAML
  use the field names of the matching REST route (the help names it, such as
  `GET /experiments/{exp}/vms` for `vm info`), and never have color. A value
  that a command does not take is refused before the command does any work.
- `--json` is an alias of `-o json` on `settings get`, `settings list` and
  `settings db list`. `util app-json -p` is `-o json`, and
  `util role-table -p` is `-o markdown`. An alias with an `-o` value that
  asks for another format is refused. `config get -p` has no effect, because
  its JSON is always indented.
- `--dry-run` on commands that delete or change things (`experiment delete`,
  `config delete`, `config apply`, `image delete`, `disk delete|commit|rebase|rename|resize`,
  `vm kill|shutdown|reset-disk`, `vm capture stop*`, `vm snapshot delete`,
  `vm forward remove`, `vm net disconnect`, `vlan alias --unpin`,
  `vlan range --clear`, `vlan release`,
  `vlan blacklist remove`, `admin user|role delete`, `settings unset`,
  `job cancel`, `experiment files delete`, `experiment trigger --cancel`,
  `experiment scorch --cancel|--clear`) does the reads and checks of a real
  run, prints one `would <action> <item>` line for each change, changes
  nothing, and exits 0 when there is no error. `admin user delete` and
  `admin role delete` with `--dry-run` read only the store and do not call
  the server. `--dry-run` of `experiment start|restart` and `image build`
  has its older meaning (do everything but call minimega or vmdb2), and
  `workflow apply -n` checks through the server.
- `-O/--output-file <path>` names the local file of a command that writes
  file data: `vm capture stream`, `vm screenshot`, `logs download`,
  `experiment files get|archive` (required for `archive`) and
  `experiment netflow download`. Without it, they write to stdout. A command
  that writes binary data refuses a stdout that is a terminal: give `-O` or
  redirect stdout. `image build -o` stays the output directory of the image.
- Shell completion (`phenix completion`) completes the arguments and flag
  values of every command, such as experiment, VM, interface and config names.
- `experiment list|apps|schedulers`, `image list` and `version` take no
  arguments and refuse extra ones.
- Commands that need state that lives only in `phenix ui` call it over
  `--unix-socket` as `global-admin`: `job`, `workflow apply`, `logs`,
  `experiment netflow`, `vm forward`, the changes of `admin`,
  `experiment trigger --cancel` and `experiment scorch` (its `--status`,
  `--cancel`, `--cleanup` and `--clear` need the server). Without a server
  they fail with `phenix ui is not running (no socket at ...)`.

## `phenix config` — manage stored configs (topology/scenario/experiment/image/user/role)

```bash
phenix config list <kind|all> [-o table|json|yaml]  # table of stored configs; JSON/YAML as GET /configs, without the spec
phenix config get <kind>/<name> [-o yaml|json]      # dump a config
phenix config create </path/to/file.yaml> ...       # create from file(s) or dir; validates against its apiVersion's schema
phenix config create --skip-validation <file>       # skip schema validation
phenix config apply -f <file|dir> [-f ...] [--dry-run]  # create new configs, update changed ones, keep equal ones
phenix config diff <file|dir>...                    # unified diff from each stored config to the file; exit 0 same, 1 differs, 2 error
phenix config edit <kind>/<name> [--force]          # open in $EDITOR
phenix config delete <kind>/<name> ... [--dry-run]  # delete one or more specific configs by kind/name
phenix config delete all [kind] [--dry-run]         # delete every stored config, or every config of one kind
```

`config apply` applies Topology, then Scenario, Experiment, Image and Role
configs, and never User configs (their files lack the password hash and
tokens). It and `config diff` compare `apiVersion`, `kind`, name,
annotations and spec, and ignore `created`, `updated`, `revision`, `status`
and the experiment name. phenix adds values to an Experiment config when it
stores it, so apply an experiment file that `config get` wrote. An update
goes through the checks of `config edit`, refuses a running experiment's
config, and refuses a config that changes while the command runs. `apply`
tries every config, prints one line for each, and fails when one failed.
`config delete all` leaves the config of a running experiment.

Stored configs carry `metadata.revision`, set by the store on every write and
ignored on input. `config edit` refuses to save if the config was written
while the editor was open ("changed while you edited it"); run it again
to edit the newer version.

## `phenix experiment` — experiment lifecycle

```bash
phenix experiment list [-o table|json|yaml]
phenix experiment apps                              # list available apps
phenix experiment schedulers                        # list scheduling algorithms
phenix experiment create <exp> -t <topology> [-s <scenario>] [flags]
phenix experiment edit <exp> [--force]              # --force overrides the running-experiment check
phenix experiment delete <exp> [-f|--force] [--dry-run]  # -f stops a running experiment before deleting it, forcing the stop
phenix experiment schedule <exp> <algorithm>         # e.g. round-robin, isolate-experiment, subnet-compute
phenix experiment start <exp|all> [--dry-run] [flags]
phenix experiment stop <exp|all> [-K] [-f|--force]   # -f marks it stopped even if minimega cannot kill its VMs
phenix experiment restart <exp> [flags]              # the flags of stop (-f, -K) and of start
phenix experiment reconfigure <exp>
phenix experiment trigger <exp> <lifecycle> [app ...]  # re-fire a lifecycle stage for app(s)
phenix experiment trigger <exp> --cancel [app ...]   # stop the triggers that phenix ui runs (needs phenix ui)
phenix experiment scorch <exp> [-r|--run <id>]      # run SCORCH run <id> (default 0) and wait until it ends
phenix experiment scorch <exp> -r <id> --status|--cancel|--cleanup|--clear  # needs phenix ui
phenix experiment soh [exp] [--status S]            # state of health summary, or one experiment's VMs and failing checks
phenix experiment captures <exp>                    # running packet captures, as vm capture list
phenix experiment files list|get|checksum|delete|archive <exp> ...  # the experiment's files, as its Files tab
phenix experiment netflow start|stop|status|captures|download <exp> ...  # netflow capture (needs phenix ui)
```

`files get` and `files delete` refuse a capture that minimega still writes.
`files get -O` writes next to the path, then replaces it only when the copy
is complete. `files archive -O <zip>` has the limits of the Files tab's
archive (500 files, 2 GiB). A file it leaves out is named in `MISSING.txt`,
and the command then fails. `files list --filter` takes the Files tab search
syntax (`"packet and size > 1mb"`). `files delete` deletes from every mesh
node. `netflow download --format text|csv|jsonl` writes a recorded capture by
the ID that `netflow captures` prints.

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

Experiment names `all`, `minimega`, `__phenix__` (any letter case), `.` and
`..` are reserved: `create`, `config create` and the API refuse them.

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
`runPeriodically` interval. `--vlan-min`/`--vlan-max` (1-4094, 0 keeps the
experiment's value) set the VLAN range of this start only.

`trigger` takes the experiment, then the lifecycle stage; each stage has a
shorthand: `configure` (`config`), `pre-start` (`pre`), `post-start` (`post`),
`running` (`run`), `cleanup` (`clean`). Passing `all` instead of an experiment
name triggers the stage in every experiment, and passing no app names triggers
every app. The `running` stage is skipped for stopped experiments. Triggering
`configure` is not equivalent to `phenix experiment reconfigure`: it does not
validate the experiment config, run config hooks, or guard against running
experiments. The command runs the stage in its own process, and Ctrl+C stops
it. `--cancel` (no lifecycle) stops the triggers that `phenix ui` runs for
the experiment. Without app names, it also ends the experiment's
`experiments.periodic` job (see [`jobs.md`](jobs.md)).

```bash
phenix experiment trigger my-exp running             # every app's running stage
phenix experiment trigger my-exp post startup        # post-start stage for one app
phenix experiment trigger my-exp --cancel soh        # stop the soh trigger that phenix ui runs
```

The old order, `trigger <lifecycle> <exp> [app ...]`, and
`trigger-running <exp> [app ...]` are deprecated. Both still work, with a
warning on stderr. The old order works only when no experiment has the
lifecycle as its name.

`scorch` runs on the server when `phenix ui` runs, so the web UI shows the
run and can cancel it, and Ctrl+C cancels it on the server. It prints the
progress and exits non-zero when the run fails. Without a server it runs in
its own process, which the web UI does not show.

## `phenix workflow` — deploy a topology directory through the running server

```bash
phenix workflow apply <DIR|NAME>                    # stage injects, upsert configs, apply the workflow config
phenix workflow apply <DIR|NAME> -n                 # --dry-run: validate everything, log the server's plan, change nothing
phenix workflow apply <DIR|NAME> -f                 # --force: allow restarting a running experiment
phenix workflow apply <DIR|NAME> -b <name>          # --branch-name (or --name): workflow branch name (default: the directory's name)
phenix workflow apply <DIR|NAME> -c <file>          # --config: workflow config, relative to DIR unless absolute
phenix workflow apply <DIR|NAME> --follow-inject-symlinks # follow symlinks in phenix-injects/ and copy as regular files
```

The argument, the directory layout, the steps, the workflow config, and the
troubleshooting table are in [`workflow.md`](workflow.md).

## `phenix job` — follow the running server's background jobs

```bash
phenix job list [--experiment X] [--kind K] [--active] [-o table|json|yaml]  # every user's jobs, newest first
phenix job get <ID> [-o table|json|yaml]  # the job and each item's outcome, error and retryable flag
phenix job cancel <ID> [--dry-run]  # queued: canceled at once; running: stops when its work notices
phenix job wait <ID> [--timeout D]  # poll every second; exit 0 succeeded, 2 partial, 1 failed/canceled/timeout
```

Talks to `phenix ui` over `--unix-socket` as Global Admin; without a server it
fails with `phenix ui is not running (no socket at ...)`. Only the web UI, the
REST API and a start through the server (`experiments.periodic`) start jobs.
The CLI's `vm` and disk commands run in-process and are not jobs. See
[`jobs.md`](jobs.md).

## `phenix vm` — manage running VMs within an experiment

```bash
phenix vm info <exp> [vm] [--host h] [-o table|json|yaml]  # table of VM(s); --host: only VMs on that host; JSON/YAML as GET /experiments/{exp}/vms
phenix vm pause|resume|restart|shutdown|kill <exp> [vm]
phenix vm reset-disk <exp> [vm]
phenix vm redeploy <exp> [vm] [flags]                # new CPU, memory or disk; unset -c/-m keep the current value; -d takes a disk spec <path>[,<interface>][,<cache>], replaces only the first disk and keeps the interface and cache mode it does not set; -r recreates the disk snapshot and injections
phenix vm set <exp> [vm] [flags]                     # change VM settings, labels, annotations and interface VLANs
phenix vm exec <exp> <vm> [--timeout D] [--] <command> [args...]  # run a command through miniccc; exits with its exit code
phenix vm cp <exp> <src> <dst> [--timeout D]         # copy one file in or out; the VM side is <vm>:<absolute path>
phenix vm net connect <exp> <vm> <iface index> <vlan id>
phenix vm net disconnect <exp> <vm> <iface index>
phenix vm capture list <exp> [vm]                    # running captures (also phenix experiment captures <exp>)
phenix vm capture start <exp> <vm> <iface index> <output file>
phenix vm capture start-subnet <exp> <subnet> [-f filter]
phenix vm capture start-vlan <exp> <vlan alias> [-f filter]   # every running VM's interfaces on the VLAN, e.g. EXP_1
phenix vm capture stop <exp> <vm> [iface]           # iface name or index stops only that one
phenix vm capture stop-subnet <exp> <subnet> [-f filter]
phenix vm capture stop-vlan <exp> <vlan alias> [-f filter]    # only the captures on the VLAN's interfaces
phenix vm capture stop-all <exp>
phenix vm capture stream <exp> <vm> <iface> [-O file] [--from-now]  # running capture as pcap, e.g. | wireshark -k -i -
phenix vm screenshot <exp> <vm> [-O file.png] [--size N]  # PNG of the screen; --size: longest side in pixels
phenix vm qos get|set|clear <exp> <vm> [iface] [--loss P] [--delay D] [--rate R]  # link quality; a rate cannot go with a loss or a delay
phenix vm cdrom insert <exp> <vm> <iso path>         # path on the node that runs the VM
phenix vm cdrom eject <exp> <vm>
phenix vm forward list|add|remove <exp> <vm> [port|vnc] [--host H] [--source-port P]  # port forwards (needs phenix ui)
phenix vm memory-snapshot <exp> <vm> <path>        # relative to the experiment files; a VM off the headnode needs a path in them
phenix vm snapshot list <exp> <vm>                   # disk+memory snapshots, one name per line (e.g. web__before-update)
phenix vm snapshot create <exp> <vm> <name>          # bare file name; prints <vm>__<name>; VM must run with snapshot flag and one disk
phenix vm snapshot restore <exp> <vm> <snapshot>     # name as list prints it; VM off the headnode can fail with minimega -headnode (#153)
phenix vm snapshot delete <exp> <vm> <snapshot>      # from every node; the experiment does not have to run
phenix vm commit <exp> <vm> [image name]             # new backing image from the first disk only; default <base>_<time>.qc2; prints the name
phenix vm mount <exp> <vm>                           # mount a running VM's filesystem on the headnode
phenix vm unmount <exp> <vm>
```

The `vm capture` `-f/--filter` value is the VM search syntax of the web UI
(`running and web`), not a minimega expression. Pausing a VM (CLI, UI or
API) leaves its captures running.

`vm exec` and `vm cp` need the miniccc agent in the VM. `exec` runs without a
shell: use `-- sh -c '...'` for pipes, and `--` before a command that has
flags. Both refuse an argument with a `$` variable or a `file:`, `http:`,
`https:` or `tar:` URL, which minimega would change on the host. `cp` out of
the VM works only for a VM on the headnode, and the copy gets mode 0600.

`vm set` on a running experiment changes only labels (`-L`,
`--append-labels`), annotations (`--annotation`, which replaces them all)
and interface VLANs (`--interface <index>=<vlan>`, or `<index>=` to
disconnect, only on a running experiment). `--host ""` lets the scheduler pick
the host, and `--partition 0` turns off disk injection.

`vm forward` acts as `global-admin` through the server: `list` shows the
forwards of every owner, and `remove` removes only those of `global-admin`.

`phenix vm snapshot create|restore` and `phenix vm commit` need a running
experiment and VM with its snapshot flag set (`vm.ErrSnapshotDisabled`
otherwise). Snapshots refuse a VM with more than one disk
(`vm.ErrMultipleDisks`, #145); `commit` writes only the first disk and warns.
They call the same `api/vm` functions as the REST routes, but in the CLI
process, so they do not take the per-VM lock of `phenix ui`. The REST routes
answer 409 for these errors.

`phenix vm mount` mounts to `<mount-dir>/<experiment>/<vm>`, and takes no host
path. `<mount-dir>` comes from the `--mount-dir` root flag
(`PHENIX_MOUNT_DIR`), defaulting to `<base-dir.phenix>/mounts`.

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
phenix image list [-o table|json|yaml]
phenix image create <name> [flags]
phenix image create-from <existing> <new>
phenix image edit <name>
phenix image build <name> [flags]
phenix image delete <name>... [--dry-run]           # carries on past a failure, then names each one
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
phenix vlan alias <exp> <alias name> <vlan id>   # pin the alias to an ID (stopped experiment; -f skips the conflict checks)
phenix vlan alias <exp> <alias name> --unpin     # clear the pin; minimega gives an ID at start
phenix vlan range [exp]                          # list VLAN ranges (all experiments, or one)
phenix vlan range <exp> <range min> <range max>  # set the range (stopped experiment; -f allows an overlap)
phenix vlan range <exp> --clear                  # clear the range
phenix vlan list [-e exp|namespace] [--vlan ID] [--state pinned|allocated|blacklisted] [--issues] [-o table|json|yaml|csv]
phenix vlan free [--count N] [--min A] [--max B] [--outside-ranges]  # first block of free IDs
phenix vlan check <exp> [--alias A=ID ...] [--range MIN-MAX]  # issues the pins and range would have; changes nothing
phenix vlan blacklist                            # list the minimega VLAN blacklist
phenix vlan blacklist add <vlan id>              # refuses an ID that a namespace gives an alias
phenix vlan blacklist remove <vlan id>           # needs minimega `clear vlans blacklist <vlan>`
phenix vlan release <namespace>                  # clear the aliases of a namespace that no experiment has
phenix vlan pin-live <exp>                       # copy minimega's live IDs of a running experiment into its spec
```

`phenix vlan list` merges the spec of each experiment with minimega
(`namespace`, `namespace <ns> vlans`, `vm info`, `vlans blacklist`). It has
one row for each owner, alias and VLAN ID, and a second table of issues:
`duplicate-pin`, `pin-blacklisted` and `unowned` (conflicts), `id-differs`
and `minimega-only` (differences, running experiments only), and
`range-overlap` and `out-of-range` (warnings). When minimega does not answer,
the table shows the phēnix side only. `release` refuses a namespace that an
experiment has, or whose VMs are on its IDs.

`vlan alias` pins only an alias that the stopped experiment has. It refuses
an ID that another experiment or namespace uses, that minimega blacklists, or
any pin while minimega does not answer. `--force` pins it anyway. `vlan range`
refuses a range that leaves out a pinned alias, and one that overlaps another
experiment's range unless `--force`. `vlan check` starts from the stored pins
and range, so the experiment does not have to exist. `--alias A=0` checks
the alias with no pin.

## Other subcommands

```bash
phenix mm <minimega args>...     # pass raw commands to (or attach to) minimega
phenix settings list|get|set|unset [key] [value]  # phenix ui applies log.*, ui.logs.level, users, experiment.disk-check at once; others on restart (it logs which)
phenix settings db list|edit ...  # legacy BoltDB-backed settings; db edit refuses the Logging category (use settings set log.system.*)
phenix disk list|details|checksum|snapshot|commit|rebase|resize|clone|rename|delete ...  # the Disks page, in-process
phenix host list                  # cluster nodes, as the Hosts page; asks minimega in-process
phenix logs [--since 1h | --start T --end T]  # phenix ui system log entries, oldest first (needs phenix ui)
phenix logs files                 # the phenix and minimega log files and whether the server can read them
phenix logs download <phenix|minimega> [-O file]
phenix admin user list|get|create|update|delete ...  # list/get read the store; changes go through phenix ui
phenix admin role list|get|create|update|delete ...  # custom roles; --policy 'resources=..;verbs=..[;names=..]'
phenix admin token create <user> [--lifetime 24h|N] [--description D]  # token to stdout, expiry to stderr
phenix ui                         # run the phenix web UI/API server
phenix healthcheck [--ready] [--url URL] [-o json]  # probe the UI server's /healthz (or /readyz); exit 0 on 200
phenix doctor [-o json] [--timeout 3s] [--skip ID|GROUP,...] [--strict] [-x] [-p] [--report]  # offline; prints a header (phenix/minimega versions, OS/kernel, uid/gid, cwd, config file, container), the settings with source (secrets redacted; -x omits), then checks: config syntax/keys, config-paths (every config.*/users.* in ., ~/.config/phenix unless root, /etc/phenix; phenix reads the first of each name, and another found warns), settings-file (the file settings set writes; warns when it is not the file read, not YAML or not writable), each set PHENIX_*/PATH/EDITOR/SUDO_* variable (parsed as phenix does; unknown PHENIX_* and ones overriding a different config file value warn), proxy-env (env group: HTTP(S)_PROXY in either case set and a set NO_PROXY/no_proxy not covering localhost, 127.0.0.1, ::1, the host name and the ui.listen-endpoint host warns; forms *, .domain, *.domain, CIDR, host:port as Go reads them; names variables, never values), the settings every command reads (bridge-mode, deploy-mode, experiment.disk-check, color, log.level, log.system.*), store/log/base dirs, base-dir.builder-templates (dirs group, warn only: not a directory, unreadable, relative, set but missing, or template files phenix ui skips, named but never why; a missing default <base-dir.phenix>/builder/templates passes, since phenix ui only reads it at start), store-file (reads only the first two pages, never bbolt.Open; not BoltDB, another version or byte order, truncated, or both meta pages corrupt fail; one corrupt meta page warns), store-owner (another owner warns unless root; as root under sudo phenix, an owner other than SUDO_USER warns), store-root (a per-user default store while /etc/phenix/store.bdb exists warns), phenix ui startup (auth: JWT key incl. dev|<user>|<role> fields, jwt-lifetime under 1m, ui.users entries parsed as phenix ui does and roles vs built-in roles, default admin user, proxy-jwt on a wildcard listen address, ignored proxy header; tls: half-set or unloadable key pair; ui.tls-cert (only with both set): cert expired/not yet valid fails, expiring within 30 days, no DNS/IP SANs, or SANs not covering the ui.listen-endpoint host (else os.Hostname; a short name matches its FQDN SAN) warn, names redacted by --report; listen and file-server endpoints; socket: unix-socket directory writable, only a warning when a socket is already there; unix-socket-gid: the user is root or in the group, named or not, and as root a gid with no group warns), server (GET /api/v1/options on unix-socket as a client only, never creating or removing it: no socket/stale socket/non-socket file/timeout warn, EACCES fails; X-Phenix-Version differing from this build and a bridge-mode or deploy-mode set here and differing from phenix ui's warn, naming the mode commands use), stuck-status (server group, warn only, store read-only: app running flags (appRunningStageStatus, SCORCH too, with its runID) and delayed starts of a running experiment with no phenix ui answering and no other phenix process in /proc naming the experiment warn, and an interrupted start warns; so does a stored start marker (status.starting) whose process on this host is not phenix, or from another host and 24h old; phenix ui clears the flags and stale start markers and resumes delayed starts as it starts; stopping lives only in phenix ui memory and jobs do not cover starts, app stages or SCORCH runs), ui-limits (server group, Linux only: phenix ui pid from SO_PEERCRED on unix-socket, else /proc cmdline phenix ui; more than 80% of its soft open-files limit open in /proc/<pid>/fd warns; unreadable /proc/<pid> or no phenix ui on this host skips), minimega (version; a failed dial names no socket, stale socket or wrong user), minimega-env (global args: -filepath must exist and be readable for phenix, warns when not writable; -base vs base-dir.minimega; degree, abssnapshot and mesh size), minimega-limits (Linux only, rows open files and memory, warn only: minimega's pid from <-base>/minimega.pid; its /proc/<pid>/limits soft open-files limit below 64 + 5 per VM on this node (QMP, VNC, cc, two qemu pipes) or more than 80% of it open in /proc/<pid>/fd; MemAvailable under 10% of MemTotal, swap over 50% used, or the vm info memory of the VMs on this node over MemTotal + SwapTotal; an unreadable /proc/<pid> or a minimega in another container skips), state-drift (rows experiments and namespaces: stored running experiments with no minimega namespace or no VMs, and namespaces with VMs that no stored experiment owns, warn), vlans (rows pinned and free: two stored experiments pinning one VLAN ID warn; a stopped experiment whose range (vlans.min/max, else minimega's) has too few free VLANs (namespace <ns> vlans, vlans range, vlans blacklist), whose range overlaps another namespace's, or whose pinned VLAN another namespace uses warns), bridge-trunk (Linux only: in a mesh of more than one node (mesh list all), an experiment bridge (phenix, or one with VM taps) with no untagged physical NIC, bond, VLAN device or tunnel port warns; one node skips with "one node, no trunk needed"; use-gre-mesh passes), schedule-hosts (spec.schedules naming a node not in mesh list all, without hostname-suffixes, warn), cc-clients (a RUNNING VM of a running experiment, up 5 minutes or more, that needs miniccc (startup via C2, a delay.c2 target, or the soh app) with no uuid in namespace <ns> cc clients warns; so does a "mismatched miniccc version" line for a current client, logged after its VM launched, in the last MiB of minimega's -logfile; a relative -logfile is not read), tools (qemu-img and vmdb2 missing fail, filebeat missing warns, disk warns at 90% and fails at 98% full; Linux-only host checks of the head node: kernel-modules (no /lib/modules/<uname -r> or /var/run/reboot-required warns; a container without /lib/modules skips), nbd (minimega disk inject for injects and inject-miniexe: no qemu-nbd fails; nbd not in lsmod, built in, max_part missing or 0, no free /dev/nbdN (no /var/lock/qemu-nbd-nbdN and no /sys/block/nbdN/pid), no /dev/nbd* in a container, and devices a failed inject can leave (lock file without pid, dead pid, a qemu-nbd with its image and mount) warn), openvswitch (no ovs-vsctl fails, or skips in a container; ovs-vsctl --timeout=<s> show failing or timing out fails, permission denied warns; ovs-vswitchd not running and no openvswitch module warn; reports the OVS version and whether bridge phenix exists), image-build (rows tools, root, loop, keyrings: missing debootstrap/kpartx/losetup/qemu-img, no CAP_SYS_ADMIN, no free loop device and no /dev/loop-control, and a missing debootstrap script or keyring for a stored image config's release warn; the keyrings row opens the store read-only with bbolt, waiting at most --timeout for the lock, and skips when locked)), disk-chains (images group: reads only the headers of the images in minimega's -filepath, or <base-dir.phenix>/images without one, within --timeout; a missing backing file, a loop or a chain over 64 deep warns, naming at most 5 images; an unlistable dir or running out of time warns; a missing dir gives a skip row), disk-formats (images group, from the same header scan as disk-chains: qcow2 version 2, qcow version 1, vmdk, qed, and images qemu refuses (unknown version or feature bits, AES or LUKS encryption, a corrupt mark) warn, naming at most 5; a second row warns when the local qemu-img is older than a feature needs: zstd 5.1, extended L2 5.2, external data file 4.0; never fails), writable-images (images group: a VM with snapshot: false writes to its drive images; warns when such an image backs another image in the images dir (from the disk-chains scan) or when more than one VM uses it), leftovers (images group, rows snapshots, files, mounts, taps, mirrors; all warn only: <head node>_<exp>_<vm>_snapshot files in the images dir of experiments not in the store, <base-dir.phenix>/experiments/<name> and <images>/<name> (with files/, miniccc_responses/ or a minirouter-<vm> file) dirs of deleted experiments with sizes, 9p mounts under mount-dir of deleted experiments or VMs minimega does not run, mega_tap* devices in /sys/class/net and OVS ports with no VM or host tap in any minimega namespace (namespace <ns> vm info/tap/capture) or no network device, mirror-<tap> OVS mirrors with no capture; what belongs to a namespace with a running minimega VM is kept; in a container, taps are checked only when /sys/class/net shows a mega_tap, an OVS bridge or ovs-system; a part without the store (also no store file), minimega or ovs-vsctl skips with the reason), disk-space (images group: the filesystems of the images dir, minimega's -base and -logfile, once each; free < max(10%, 5 GiB) warns, naming the minimega log size and the size leftovers found; an inject dirs row warns about dstImg* dirs in -base and says which are mounted), image-locks (images group, Linux only: qemu-system/qemu-nbd/qemu-img processes in /proc/<pid>/fd that hold an image or backing file of a stopped experiment and are not VMs of a running one warn: any open of a snapshot: false image, a write open of the others; skips in a container that shows no minimega process; writable-images, leftovers, image-locks and inject-partitions share one read-only bbolt open of the store per run, waiting at most --timeout for the lock), inject-partitions (images group, warn only: for each VM with snapshot and a first-drive inject_partition other than 0 that gets injects, deletions or startup-app scripts, reads the partition table of that image once per run (at most 16 MiB; qcow2 chains through qemu-img dd -U; Linux numbering: MBR logicals from 5, GPT entry number); no partition table, no partition 1 (minimega waits for it, no partitions found on image), no partition N (desired partition N not found), an extended partition, swap/LVM/LUKS/ISO 9660/squashfs/no known file system, or a partition under 1 GiB while a larger Linux or NTFS one exists warn, with the partitions found and the largest Linux or NTFS partition as the inject_partition to try; an NTFS inject partition with VOLUME_IS_DIRTY in $Volume or a $LogFile restart area not clean (Windows hibernated or Fast Startup; no hiberfil.sys lookup), or an ext superblock with the error state, warns, read with at most 512 KiB more per image; a missing image, or a qcow2 without qemu-img, is not checked), features (tunneler-download and webshark say whether phenix ui turns them on, relative to the working directory; ui.features warns on an unknown name with a did-you-mean and on tunneler-download/file-server set without what they need, fails on webshark without a build, which phenix ui refuses; ui.logs.minimega-path missing, or unset while minimega logs to a file, warns), apps group (apps lists phenix-app-*/phenix-scorch-component-* on PATH; scenario-apps: an app or SCORCH component type (type, else name) of a stored scenario or experiment that is not built in (apps ntp, serial, startup, vrouter, tap, soh, scorch; components break, pause, soh, tap) and not on PATH warns; app-runtimes: such a program that this user cannot run, a #! interpreter that does not exist or cannot run, an env program not on PATH, or a #! line with a carriage return warns), and stored-config checks, all warn only and skip without the store: scenario-hosts (config: a scenario app hosts[].hostname that is not a node of the experiment's topology), inject-sources (files: an injection src that does not exist; experiments only for VMs with snapshot and an inject partition other than 0, relative src under baseDir, absolute src under baseDir not checked since apps write it at the start; topologies only absolute src), image-configs (tools: an overlay dir that does not exist; script files not on disk only noted, since the config holds their copies), rbac-users (auth, only with ui.jwt-signing-key set: a user rbac.roleName that is not a stored or built-in role, and literal resourceNames (no glob or !) in experiment/vm or user policies naming configs not in the store; never prints passwords or tokens), store-fs (files: the store dir on NFS/SMB/CIFS/Ceph/9p/Lustre/GPFS/GFS2/AFS or FUSE warns; a store over 100 MiB with over 50% free pages, read from the meta page and the free list header without the lock, warns with bbolt compact; Linux only for the filesystem type), secret-files (files: the TLS key, config/users files that set ui.jwt-signing-key or ui.users, and the store file readable by group or others (mode & 044) warn, and the key or config files owned by another user than root or the phenix user warn; stat only, never the contents); the store-reading checks open bbolt once per kind per run, and after one lock timeout the others do not wait; then a summary with a hint per failure; --color colors the statuses, the summary (worst status) and the hints, never with -o json or --report. --skip takes check ids or groups (config, env, settings, files, dirs, ui, auth, tls, socket, server, minimega, tools, images, apps, features); JSON has schemaVersion 1 and a stable id per check; --report fences the output for a bug report and replaces host names, IP addresses, user names and emails with consistent tokens (<host-1>, <ip-1>, <user-1>, <email-1>; paths keep their shape) and passwords, the JWT key, secret-like key=value values and URL credentials with <redacted>, keeping localhost, 127.0.0.1, ::1, 0.0.0.0, ::, the users root/phenix/minimega/nobody/admin@foo.com and a hostname-suffixes suffix (ws42-phenix -> <host-1>-phenix); it never prints the hostname (also in <host>_<exp>_<vm>_snapshot names, the host minimega answers from, the nodes of its mesh, the nodes in spec.schedules, the ui.listen-endpoint host and the TLS certificate DNS names); -p (--problems-only) shows only warn and fail rows (JSON: checks filtered, hiddenChecks count, status from all checks), the summary still counts every check and says how many are hidden, and the exit code does not change; exit 1 on a failed check, or a warning with --strict
phenix util app-json <exp>        # print the experiment JSON a user app receives on stdin
phenix util role-table            # print every permission phenix checks, and which roles allow it
phenix completion bash|zsh|fish|powershell
phenix version [-o text|json|yaml]  # one line; JSON/YAML have the keys of GET /version
```

`disk` paths are relative to the minimega files directory, or absolute inside
it. `disk delete` refuses an image that other images or experiments use unless
`--force`. `disk rebase --unsafe` changes only the backing name, and
`--independent` (in place of a backing image) removes the backing image.
`admin user create|update` read a password from the terminal, or from stdin
with `--password-stdin` (`update --password` asks for the current password
first).

`phenix mm` is also available as `phenix minimega`. Its flags (`-n/--namespace`
to scope commands to a minimega namespace, `-a/--attach` to open the
interactive console) and any global flags must come before the first minimega
argument; everything after it is passed to minimega unchanged, so
`phenix mm -n my-exp cc exec ls -a` sends `-a` to `ls`. Arguments are quoted
for minimega as needed; a single argument is sent as-is, so
`phenix mm -n my-exp "vm info"` passes a whole minimega command line. The
command exits non-zero, and logs the error, when minimega reports an error or
the connection to minimega is lost, so scripts can check its exit status.
