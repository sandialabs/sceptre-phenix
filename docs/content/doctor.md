# Doctor

`phenix doctor` checks your phēnix configuration, the host and minimega, and
it doesn't need `phenix ui` to be running. Reach for it when the server or an
experiment won't start, when a setting seems to have no effect, after an
upgrade, and before you report a bug.

```bash
# Only the checks that warn or fail, without the settings list
phenix doctor -p -x

# In the Docker deployment
docker exec phenix phenix doctor -p -x
```

Run it as the same user, and with the same flags and environment, as the
phēnix command you're checking. doctor changes nothing: it opens the store
read-only, sends minimega only commands that change nothing, and binds no
port. The only files it writes are the ones every phēnix command creates, and
temporary files for the `inject-partitions` check. The
[`phenix doctor` CLI reference](cli/doctor.md) lists every flag.

## Examples

`--skip` takes any check or group name from the [Checks](#checks) tables, such
as on a host without minimega:

```bash
phenix doctor --skip minimega,images
```

`--timeout` is 3 seconds by default. When it runs out, a slow check such as
`disk-chains` warns, and `minimega` and `openvswitch` fail. To give them more
time:

```bash
phenix doctor --timeout 30s
```

Check a host from a script. `--strict` makes a warning exit 1 too, and the
JSON report has a hint for each warning and failure:

```bash
if ! phenix doctor --strict -x -o json > doctor.json; then
  jq -r '.checks[] | select(.status == "warn" or .status == "fail") | "\(.id): \(.hint)"' doctor.json
fi
```

List the settings that aren't at their default value, and where each value
comes from:

```bash
phenix doctor -o json --skip minimega,images | jq -r '.settings[] | select(.source != "default") | "\(.key)=\(.value) (\(.source))"'
```

## Reading the Output

doctor prints a header, every setting with its value and source, one row per
check, and a summary. It never prints the values of `ui.jwt-signing-key` or
`ui.users`. Here's the end of
`phenix doctor -p -x` on a host where minimega isn't running:

```text
CHECK               STATUS  DETAIL
ui.jwt-signing-key  WARN    empty, so phenix ui serves without authentication
minimega            FAIL    no answer on /tmp/minimega/minimega: unable to dial: dial unix /tmp/minimega/minimega: connect: no such file or directory (no socket there: minimega is not running, or base-dir.minimega names another directory)
filebeat            WARN    filebeat is not a program on PATH, so SCORCH starts no Filebeat, even for a component that enables it

FAIL: 1 failed, 2 warned, 55 passed, 24 skipped. Failed: minimega. Warned: ui.jwt-signing-key, filebeat. 79 passing or skipped checks hidden
  minimega: start minimega, or set base-dir.minimega to the directory that holds its socket
```

| Status | Meaning |
| :--- | :--- |
| `pass` | No problem found. |
| `warn` | phēnix works, but something may cause trouble later. |
| `fail` | phēnix, or part of it, won't work until you fix this. |
| `skip` | The check didn't run. The row says why. |

doctor exits 1 when a check fails (or warns, with `--strict`), and 0
otherwise. `-o json` prints the same report as JSON, with a stable `id` and
`group` for each check.

## Doctor and the Health Checks

doctor checks a host without a running server. To monitor a running
`phenix ui`, use the [health checks](health.md) (`/healthz` and `/readyz`).

## Reporting a Bug

`--report` prints the output in a fenced code block that you can paste into the
[bug report form](https://github.com/sandialabs/sceptre-phenix/issues/new?template=1-bug-report.yml):

```bash
phenix doctor --report
```

The report swaps host names, IP addresses, user names and email addresses for
tokens such as `<host-1>`, and replaces passwords, the JWT signing key and
other secrets with `<redacted>`. The same value always gets the same token, so
a path keeps its shape, such as `/home/<user-1>/images`. Values that say
nothing about your site, such as `localhost` and `root`, stay as they are.

!!! warning
    Read the output before you paste it. doctor can't replace a name it
    doesn't know about, such as another host's name in a path.

## Checks

The Check column is the name that `--skip` takes, and Group is its group. A
check that can both warn and fail is in both tables. The hint in doctor's
output gives the fix, often with the exact command.

### Failures

| Check | Group | Fails when | Fix |
| :--- | :--- | :--- | :--- |
| `config-file`, `users-file` | `config` | The file can't be read or isn't valid YAML. | Fix the YAML at the line the row names. |
| `env` (one row for each variable) | `env` | phēnix would refuse or misread a value, or `EDITOR` isn't on `PATH`. | Fix or unset the variable. |
| `bridge-mode`, `deploy-mode`, `experiment.disk-check`, `color` | `settings` | Every command refuses the value. | [Set a value that the hint lists](troubleshooting.md#every-command-fails-on-a-mode-setting). |
| `log.system.max-size` | `settings` | The value is negative, so every write to the system log fails. | Set a whole number of megabytes, or `0` for 100. |
| `store.endpoint` | `files` | This user can't write the store file or its directory. | Create the directory, or set a `bolt://` path this user can write. |
| `store-file` | `files` | The store isn't a whole BoltDB file. | Restore a backup, or move it so phēnix creates an empty store. |
| `log.system.path`, `log.console` | `files` | This user can't write or create the log file. | Use a file or directory this user can write. |
| `base-dir.phenix`, `base-dir.minimega`, `base-dir.injects`, `base-dir.topologies`, `mount-dir` | `dirs` | The path isn't a directory. | Point the setting at a directory this user can use. |
| `ui.shutdown-grace-period`, `ui.netflow.max-size`, `ui.captures.max-size` | `ui` | `phenix ui` would refuse the value. | Set a whole number: seconds, or MiB for the sizes. |
| `ui.jwt-signing-key` | `auth` | A development key doesn't name a user and a role. | Write it as <code>dev&#124;&lt;user&gt;&#124;&lt;role&gt;</code>. |
| `ui.jwt-lifetime` | `auth` | The value is under a minute, such as `24` (nanoseconds). | Use a unit, such as `24h`. |
| `ui.users` | `auth` | An entry isn't `name:password:role[:resource...]`, so `phenix ui` skips it. | Fix the entry. |
| `ui.proxy-auth-header` | `auth` | The value isn't an HTTP header name. | Fix the name, or unset `ui.proxy-auth-header`. |
| `ui.tls` | `tls` | Only one of `ui.tls-key` and `ui.tls-cert` is set, or they don't load. | Set both to a readable key and its certificate, or neither. |
| `ui.tls-cert` | `tls` | The certificate has expired or isn't valid yet. | Renew it, then restart `phenix ui`. |
| `ui.listen-endpoint`, `ui.file-server-endpoint` | `tls` | `phenix ui` can't listen on the value. | Use `host:port`, such as `0.0.0.0:3000` (`0` turns the file server off). |
| `unix-socket` | `socket` | The directory is missing or unwritable, or the path is too long. | Use a path in a directory this user can write. |
| `unix-socket-gid` | `socket` | This user isn't root or in the group, so `phenix ui` exits. | Use a group this user is in, run as root, or `-1`. |
| `server` | `server` | This user can't connect to the socket of `phenix ui`. | Run doctor as the user of `phenix ui`, or in its group. |
| `minimega` | `minimega` | minimega doesn't answer on its socket. | [Start minimega, or fix `base-dir.minimega`](troubleshooting.md#phenix-cannot-reach-minimega-or-its-images). |
| `minimega-env` | `minimega` | phēnix can't read minimega's `-filepath` directory. | Mount the files directory at the same path for both. |
| `qemu-img`, `vmdb2` | `tools` | The program isn't on `PATH`, or `qemu-img` doesn't work. | Install `qemu-utils` or `vmdb2`, or use `--skip vmdb2`. |
| `disk` | `tools` | A phēnix or minimega filesystem is 98% full. | Free up space. |
| `nbd` (Linux only) | `tools` | `qemu-nbd` is missing, so minimega injects fail. | [Install `qemu-nbd`](troubleshooting.md#injects-fail-with-an-nbd-error). |
| `openvswitch` (Linux only) | `tools` | `ovs-vsctl` is missing, or Open vSwitch doesn't answer. | [Install or start Open vSwitch](troubleshooting.md#open-vswitch-does-not-answer). |
| `ui.features` | `features` | `webshark` is set without a WebShark build, so `phenix ui` won't start. | List only `vm-mount`. |
| `ui.logs.minimega-path` | `features` | The path isn't a regular file. | Set it to minimega's log file. |

### Warnings

| Check | Group | Warns when | Fix |
| :--- | :--- | :--- | :--- |
| `config-file` | `config` | There is no `config.yaml`. | phēnix creates it if it can write its config directory. |
| `config-keys` | `config` | A key isn't a phēnix setting, such as a typo. | Rename or remove it. `phenix settings list` shows the valid keys. |
| `config-paths` | `config` | The search path has more than one config or users file. | Merge them into the first one, which phēnix reads. |
| `settings-file` | `config` | `phenix settings set` would write a file phēnix doesn't read, or can't. | Use a YAML config file this user can write. |
| `scenario-hosts` | `config` | A scenario app names a host that isn't in the topology. | Fix the names with `phenix experiment edit <experiment>`. |
| `env` (one row for each variable) | `env` | A `PHENIX_*` variable is empty, unknown, or overridden by a flag. | Fix or unset the variable. |
| `env-overrides` | `env` | A `PHENIX_*` variable overrides a different config file value. | Unset the variable, or change the file to match. |
| `proxy-env` | `env` | A proxy is set, and `NO_PROXY` doesn't cover `phenix ui`. | Add the named hosts to `NO_PROXY` and `no_proxy`. |
| `log.level`, `log.system.max-size`, `log.system.max-backups`, `log.system.max-age` | `settings` | phēnix can't use the value, so it falls back to a default. | Set a value from the hint. |
| `minimega.read-connections` | `settings` | The value isn't a number from 3 to 64. | Set a whole number from 3 to 64. |
| `store-file` | `files` | One of the store's two meta pages is corrupt. | Back up the store while `phenix ui` is stopped. |
| `store-owner` | `files` | Another user owns the store file. | Run phēnix as the store's owner. |
| `store-root` | `files` | This user has its own store, but `/etc/phenix/store.bdb` exists. | Use `sudo -i`, or set `store.endpoint` to `bolt:///etc/phenix/store.bdb`. |
| `store-fs` | `files` | The store is on a network filesystem, or is mostly free pages. | [Move or compact the store](troubleshooting.md#the-store-is-on-a-network-filesystem-or-has-many-free-pages). |
| `secret-files` | `files` | Other users can read a file that holds a secret. | [`chmod 600` the file](troubleshooting.md#files-that-hold-secrets-are-readable-by-other-users). |
| `inject-sources` | `files` | The source file of a node injection doesn't exist. | Put the file there, or change the injection `src`. |
| `base-dir.phenix`, `base-dir.minimega`, `base-dir.injects`, `base-dir.topologies`, `mount-dir` | `dirs` | The directory is missing, relative, or unusable by this user. | Create it with access for this user, or use another one. |
| `base-dir.builder-templates` | `dirs` | The directory is unusable, or `phenix ui` skips a template file. | Fix the directory or file. The `phenix ui` log says why. |
| `ui.jwt-signing-key` | `auth` | The key is empty (no sign-in) or a development key. | Set a long random secret to require sign-in. |
| `ui.users` | `auth` | A role is unknown, or the list is empty with sign-in on. | Fix the role, or change the default user's password. |
| `proxy-jwt` | `auth` | `phenix ui` listens on every address, but doesn't verify proxy JWTs. | Listen only where the proxy reaches, such as `127.0.0.1:3000`. |
| `ui.proxy-auth-header` | `auth` | With no sign-in or a dev key, `phenix ui` ignores the header. | Set `ui.jwt-signing-key`, or unset `ui.proxy-auth-header`. |
| `rbac-users` | `auth` | A user's role is missing, or a role names deleted items. | Fix the role on the Users page or with `phenix config edit`. |
| `ui.tls-cert` | `tls` | The certificate expires within 30 days, or doesn't name this host. | Renew it with the right names, then restart `phenix ui`. |
| `ui.listen-endpoint` | `tls` | The endpoint has no port. | Use `host:port`, such as `0.0.0.0:3000`. |
| `unix-socket` | `socket` | The path is relative, or holds a non-socket or another user's socket. | Use an absolute path in a directory this user can write. |
| `unix-socket-gid` | `socket` | No group has the id (as root), or a deprecated key sets it. | Use an existing group in `ui.unix-socket-gid`, or set `-1`. |
| `server` | `server` | No `phenix ui` answers, or its version or modes differ. | Start `phenix ui`, or match its version and modes. |
| `stuck-status` | `server` | The store shows runs in progress that nothing is running. | [Start or restart `phenix ui`](troubleshooting.md#apps-or-a-scorch-run-stay-marked-as-running). |
| `ui-limits` (Linux only) | `server` | `phenix ui` uses over 80% of its open files limit. | Raise `LimitNOFILE=` or `--ulimit nofile`, then restart it. |
| `minimega-env` | `minimega` | `-filepath` is relative or read-only, `-base` differs, or the mesh fails. | Use an absolute `-filepath`, and match `base-dir.minimega` to `-base`. |
| `experiment-files` | `minimega` | `<base-dir.phenix>/images` isn't minimega's `-filepath`. | Make the two the same directory. |
| `minimega-limits` (Linux only) | `minimega` | Open files, or memory and swap, are too low for the VMs. | [Raise the limit, or stop VMs](troubleshooting.md#minimega-runs-out-of-open-files-or-memory). |
| `state-drift` | `minimega` | The store and minimega disagree on which experiments run. | [Stop the experiment, or clear the namespace](troubleshooting.md#experiments-show-as-running-after-minimega-restarts). |
| `vlans` | `minimega` | Two experiments pin a VLAN ID, or one can't get its VLANs. | [Change the pin, or stop the other experiment](troubleshooting.md#an-experiment-cannot-get-its-vlans). |
| `bridge-trunk` (Linux only) | `minimega` | A mesh bridge has no trunk or tunnel, so nodes can't connect. | [Add a trunk, or set `use-gre-mesh`](troubleshooting.md#vms-on-different-nodes-cannot-reach-each-other). |
| `schedule-hosts` | `minimega` | An experiment schedules a VM on a node not in the mesh. | Fix the schedule, or add the node to the mesh. |
| `cc-clients` | `minimega` | A VM's miniccc client is missing after 5 minutes, or mismatched. | [Inject a matching miniccc](troubleshooting.md#a-vm-has-no-miniccc-client). |
| `filebeat` | `tools` | `filebeat` isn't on `PATH`, so SCORCH starts no Filebeat. | Install Filebeat if your SCORCH components use it. |
| `disk` | `tools` | A phēnix or minimega filesystem is 90% full. | Free up space. |
| `kernel-modules` (Linux only) | `tools` | The running kernel's modules are missing, or a reboot is pending. | Reboot into the installed kernel. |
| `nbd` (Linux only) | `tools` | The `nbd` module isn't ready, or no `/dev/nbdN` is free. | [Run `modprobe nbd max_part=10` as root](troubleshooting.md#injects-fail-with-an-nbd-error). |
| `openvswitch` (Linux only) | `tools` | Permission is denied, or `ovs-vswitchd` or its module isn't running. | Run doctor as root, or start Open vSwitch. |
| `image-build` (Linux only) | `tools` | `phenix image build` lacks a program, root, a loop device, or keyring. | Install what the row names, and build as root. |
| `image-configs` | `tools` | An overlay directory of an image config doesn't exist. | Create it, or `phenix image remove <config> --overlays <path>`. |
| `disk-chains` | `images` | An image has a broken backing chain. | [Restore the backing file, or rebase the image](disks.md#broken-backing-chains). |
| `disk-formats` | `images` | An image's format is old or unsupported, or `qemu-img` is too old. | See [Image Format Versions](disks.md#image-format-versions). |
| `writable-images` | `images` | A VM with `snapshot: false` writes to a shared or backing image. | Set `snapshot: true`, or give each VM its own copy. |
| `leftovers` | `images` | Deleted experiments or stopped VMs left files, mounts, taps or mirrors. | Remove each one with the command in the hint. |
| `disk-space` | `images` | A filesystem of images or minimega has under 10% or 5 GiB free. | [Delete unused images and leftovers](troubleshooting.md#low-disk-space-and-leftovers-of-deleted-experiments). |
| `image-locks` (Linux only) | `images` | A qemu process holds an image of a stopped experiment open. | If nothing needs it, `kill <pid>` or `qemu-nbd -d /dev/nbdN`. |
| `inject-partitions` | `images` | `inject_partition` names a partition minimega can't write, or one needing repair. | [Use the suggested partition](troubleshooting.md#injects-go-to-the-wrong-partition), or repair it as the hint says. |
| `scenario-apps` | `apps` | A scenario uses an app or SCORCH component that isn't installed. | [Install it, or add it to `PATH`](troubleshooting.md#an-app-or-scorch-component-is-not-found). |
| `app-runtimes` | `apps` | An app isn't executable, lacks its interpreter, or has Windows line ends. | `chmod +x`, install the interpreter, or `dos2unix`. |
| `tunneler-download` | `features` | `downloads/tunneler` isn't a directory. | Make it a directory of `phenix-tunneler` builds, or remove it. |
| `ui.features` | `features` | A feature is unknown, is missing what it needs, or is `minimega-captures`, which has no effect. | List only `vm-mount`. |
| `minimega-captures` | `features` | doctor can't read the minimega version. | Use a minimega build that gives a version, such as `minimega 3.4.0`. |
| `ui.logs.minimega-path` | `features` | Missing or unreadable file, or unset while minimega logs to a file. | [Set it to minimega's log file](troubleshooting.md#no-minimega-logs-on-the-logs-page). |

### Other Checks

| Check | Group | What it reports |
| :--- | :--- | :--- |
| `apps` | `apps` | The user apps and SCORCH components on `PATH`. |
| `webshark` | `features` | Whether `phenix ui` serves WebShark (it does when it finds a build). |
| `minimega-captures` | `features` | Whether minimega supports experiment captures (3.4.0 or later). |

## See Also

* [phenix doctor](cli/doctor.md): every flag of the command.
* [Troubleshooting](troubleshooting.md): longer fixes for the problems that doctor reports.
* [Health Checks](health.md): the checks of a running server.
* [Settings](settings.md): where each setting comes from and what it does.
