# Doctor

`phenix doctor` checks the phēnix configuration, the host and minimega
without a running `phenix ui`. Use it when the server will not start, when an
experiment will not start, when a setting seems to have no effect, after an
upgrade, and before you report a bug. The
[`phenix doctor` CLI reference](cli/doctor.md) lists every flag.

## Examples

Show only the checks that warn or fail, without the settings list:

```bash
phenix doctor -p -x
```

Skip the `minimega` and `images` groups, such as on a host without minimega.
`--skip` takes the check and group names from the [Checks](#checks) table:

```bash
phenix doctor --skip minimega,images
```

Wait longer for minimega and the slow checks. `--timeout` is 3 seconds by
default. A slow check, such as `disk-chains` on a host with many disk images,
warns when it does not finish in time. `minimega` and `openvswitch` fail:

```bash
phenix doctor --timeout 30s
```

Check a host in a script, such as after an upgrade. With `--strict`, a warning
also gives exit status 1. The JSON report has a hint for each warning and
failure, and `jq` lists them:

```bash
if ! phenix doctor --strict -x -o json > doctor.json; then
  jq -r '.checks[] | select(.status == "warn" or .status == "fail") | "\(.id): \(.hint)"' doctor.json
fi
```

List the settings that do not have their default value, and where each value
comes from:

```bash
phenix doctor -o json --skip minimega,images | jq -r '.settings[] | select(.source != "default") | "\(.key)=\(.value) (\(.source))"'
```

## Reading the Output

doctor prints, in order:

* A header with the phēnix and minimega versions, the OS, the user, the
  config file that phēnix reads, and whether phēnix runs in a container.
* Every setting with its value and its source: `flag`, `env`, `config file`
  or `default`. doctor never prints the values of `ui.jwt-signing-key` or
  `ui.users`.
* One row for each check, with its status and what it found.
* A summary that counts the statuses and names each problem, with a hint for
  each failure.

The end of the output of `phenix doctor -p -x` on a host where minimega does
not run:

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
| `pass` | The check found no problem. |
| `warn` | phēnix works, but something can cause a problem later or work differently than you expect. |
| `fail` | phēnix, or a part of it, cannot work until you fix the problem. |
| `skip` | The check did not run. You skipped it, it does not apply to this host, or it could not get its data, such as from the store. The row tells why. |

`phenix doctor` exits 1 when a check fails, or with `--strict` when one
warns, and 0 otherwise. On a terminal, the statuses are colored (see
[Colors](settings.md#colors)). `-o json` prints the same report as one JSON
object, with a stable `id` and `group` for each check.

| Flag | What it does |
| :--- | :--- |
| `--skip <name>` | Skips a check or a group from the [Checks](#checks) table. Repeat it, or separate the names with commas. |
| `-p`, `--problems-only` | Shows only the checks that warn or fail. The summary still counts every check. |
| `-x`, `--no-print-settings` | Leaves out the settings. |
| `--strict` | Fails on a warning, and shows the hints of warnings in the text output too. |
| `-o json` | Prints the report as one JSON object. |
| `--timeout <duration>` | How long doctor waits for minimega, `phenix ui`, the store lock and each slow check (3 seconds by default). |
| `--report` | Prints the report for a bug report. See [Reporting a Bug](#reporting-a-bug). |

!!! note
    `phenix doctor` changes nothing. It opens the store read-only, sends
    minimega only commands that change nothing, and binds no port. It writes
    only what every phēnix command creates (the config directory, an empty
    `config.yaml` when it finds no config file, and the log directory), and
    the temporary files of the `inject-partitions` check.

## Doctor and the Health Checks

doctor and the [health checks](health.md) look at different things. doctor
checks a host and its configuration, and needs no running server. The health
endpoints `/healthz` and `/readyz` report on a running `phenix ui`. Use doctor
when `phenix ui` will not start, or to find the cause of a problem. Use the
health endpoints to monitor a running server.

## Reporting a Bug

`--report` prints the whole output, text or JSON, in a fenced code block to
paste into the
[bug report form](https://github.com/sandialabs/sceptre-phenix/issues/new?template=1-bug-report.yml):

```bash
phenix doctor --report
```

The report replaces host names, IP addresses, user names and email addresses
with tokens, such as `<host-1>` and `<ip-1>`. It replaces passwords, the JWT
signing key, other secrets and the credentials in URLs with `<redacted>`. The
same value always gets the same token, so a path keeps its shape, such as
`/home/<user-1>/images`. The report keeps the values that tell nothing about
your site: `localhost`, `127.0.0.1`, `::1`, `0.0.0.0`, `::`, the users
`root`, `phenix`, `minimega`, `nobody` and `admin@foo.com`, and a host name
suffix from `hostname-suffixes`, such as `-phenix` in `<host-1>-phenix`.

Read the output before you paste it. doctor cannot replace a name that it
does not know, such as the name of another host in a path.

## Checks

The `Check` column gives the name that `--skip` takes, and the `Group` column
the name of its group. The hint in the output gives the same fix, often with
the exact command.

| Check | Group | What it means | How to fix it |
| :--- | :--- | :--- | :--- |
| `config-file`, `users-file` | `config` | Fails when the file cannot be read or does not parse, so phēnix ignores it. Warns when no `config.yaml` exists. | Fix the YAML syntax at the line that the row names. Without a file, phēnix creates `config.yaml` when it can write its config directory, or you can use environment variables. |
| `config-keys` | `config` | Warns when a config file holds a key that is not a phēnix setting, such as a misspelling. | Rename or remove the key. `phenix settings list` shows the valid keys. |
| `config-paths` | `config` | Warns when the search path has more than one config or users file. phēnix reads only the first file of each name. | Merge the files that phēnix ignores into the file that it reads, then remove them. |
| `settings-file` | `config` | Warns when `phenix settings set` would write a file other than the file phēnix reads, a file that is not YAML, or a file this user cannot write. | Make the config file a YAML file that this user can write, or run `phenix settings` as its owner. |
| `scenario-hosts` | `config` | Warns when a scenario app names a host (`hosts[].hostname`) that is not a node of the topology. The app skips that host. | Change the host names in the scenario to the node host names with `phenix experiment edit <experiment>`. |
| `env` (one row for each variable) | `env` | Checks each set `PHENIX_*` variable, `PATH`, `EDITOR` and `SUDO_USER` as phēnix reads them. Fails when phēnix would refuse or misread a value, or `EDITOR` is not on `PATH`. Warns when a `PHENIX_*` variable is empty or names no setting, or a flag overrides it. | Fix or unset the variable. |
| `env-overrides` | `env` | Warns when a `PHENIX_*` variable overrides a different value in a config file. | Unset the variable to use the value of the file, or change the file to match the variable. |
| `proxy-env` | `env` | Warns when a proxy variable, such as `HTTP_PROXY`, is set and `NO_PROXY` does not hold the hosts of `phenix ui`. Apps then send their requests for phēnix to the proxy. | Add the hosts that the row names to `NO_PROXY` and `no_proxy`, or unset the proxy variables. |
| `bridge-mode`, `deploy-mode`, `experiment.disk-check`, `color`, `log.level`, `log.system.max-size`, `log.system.max-backups`, `log.system.max-age` | `settings` | Fails when a mode has a value that every phēnix command refuses, or `log.system.max-size` is negative. Warns when phēnix cannot read `log.level` or a `log.system.*` value, and uses a default in its place. | Set a value that the hint gives, such as `manual` or `auto` for `bridge-mode`. See [Settings](settings.md). |
| `store.endpoint` | `files` | Fails when this user cannot write the store file or its directory. | Create the directory, or set `store.endpoint` to a `bolt://` path that this user can write. |
| `store-file` | `files` | Fails when the store file is not a whole BoltDB file. Warns when one of its two meta pages is corrupt, so the last write can be lost. | For a warning, copy the store to a backup while `phenix ui` is stopped. For a failure, restore the store from a backup, or move it so that phēnix creates an empty store. |
| `store-owner` | `files` | Warns when another user owns the store file. | Run phēnix as the owner of the store, or give the store to the user that runs `phenix ui`. |
| `store-root` | `files` | Warns when this user uses its own default store while `/etc/phenix/store.bdb` exists. A `phenix ui` that runs as root uses that other store. | Run phēnix in a root shell (`sudo -i`), or set `store.endpoint` to `bolt:///etc/phenix/store.bdb`. |
| `store-fs` | `files` | Warns when the store is on a network or FUSE filesystem, where the file locks of BoltDB can fail. Also warns when more than half of a store larger than 100 MiB is free pages. | Put the store on a local disk. To make a large store smaller, stop `phenix ui`, keep a backup, and run `bbolt compact`. |
| `log.system.path`, `log.console` | `files` | Fails when this user cannot write or create the log file. | Set the setting to a file or directory that this user can write. `log.console` also takes `stderr` and `stdout`. |
| `secret-files` | `files` | Warns when the group or others can read a file that holds a secret: the TLS key, a config file that sets `ui.jwt-signing-key` or `ui.users`, or the store. Also warns when another user owns such a file. | Run `chmod 600 <path>` on each file that the row names, and `chown <user> <path>` to give it to the user that runs phēnix. |
| `inject-sources` | `files` | Warns when the source file of a node injection does not exist, so the start cannot copy it into the VM. | Put the file at the path, or change the injection `src` in the topology. In a container, mount the path in the phēnix and minimega containers. |
| `base-dir.phenix`, `base-dir.minimega`, `base-dir.injects`, `base-dir.topologies`, `mount-dir` | `dirs` | Fails when the path is not a directory. Warns when the directory does not exist, is relative, or this user cannot use it. | Create the directory with access for this user, or set the setting to a directory that it can use. |
| `base-dir.builder-templates` | `dirs` | Never fails. Warns when the path is not a directory, is relative, or this user cannot read it, when the setting names a path that does not exist, and when `phenix ui` skips a template file in it (the row names at most 5 files). A missing directory passes when the setting is not set. | Create the directory, fix its permissions, or set the setting to a directory of Builder templates. Fix or remove each skipped file; the `phenix ui` log gives the reason. |
| `ui.shutdown-grace-period`, `ui.netflow.max-size` | `ui` | Fails when `phenix ui` would refuse the value. | Set a whole number: seconds for the grace period, MiB for the netflow size. |
| `ui.jwt-signing-key` | `auth` | Warns when the key is empty, so `phenix ui` does not ask users to sign in, or when it is a development key. Fails when a development key does not name a user and a role. | Set a long random secret to require sign-in. Write a development key as <code>dev&#124;&lt;user&gt;&#124;&lt;role&gt;</code>. |
| `ui.jwt-lifetime` | `auth` | Fails when phēnix reads the value as less than one minute, such as `24` (24 nanoseconds) or `1d` (0). | Use a duration with a unit, such as `24h`. |
| `ui.users` | `auth` | Fails when an entry is not `name:password:role[:resource...]`. `phenix ui` skips such an entry without an error. Warns when a role is not built in or stored, or when the list is empty with sign-in on, so `phenix ui` creates the default user `admin@foo.com`. | Write each user as `name:password:role[:resource...]`, with a built-in or stored role. Change the password of the default user. |
| `proxy-jwt` | `auth` | With the `proxy-jwt` key, warns when `ui.listen-endpoint` listens on every address. `phenix ui` does not check the signatures of proxy JWTs. | Set `ui.listen-endpoint` to an address that only the proxy reaches, such as `127.0.0.1:3000`. |
| `ui.proxy-auth-header` | `auth` | Fails when the value is not an HTTP header name. Warns when sign-in is off, so `phenix ui` ignores the header. | Set `ui.jwt-signing-key`, or unset `ui.proxy-auth-header`. |
| `rbac-users` | `auth` | Warns when a stored user has a role that does not exist, or a role names an experiment, VM or user that the store does not have. | Give the user a role on the Users page, or with `phenix config edit user/<user>`. Remove the deleted names from the role. |
| `ui.tls` | `tls` | Fails when only one of `ui.tls-key` and `ui.tls-cert` is set, so `phenix ui` serves plain HTTP. Also fails when a file cannot be read, or the key and certificate are not a pair. | Set both settings to a readable key and its certificate, or set neither. |
| `ui.tls-cert` | `tls` | Fails when the certificate expired or is not valid yet. Warns when it expires within 30 days, or its names do not include the host that users connect to. | Renew the certificate, or make one whose DNS or IP names include that host. Then restart `phenix ui`. |
| `ui.listen-endpoint`, `ui.file-server-endpoint` | `tls` | Fails when `phenix ui` cannot listen on the value. Warns when the listen endpoint has no port. | Set `host:port`, such as `0.0.0.0:3000`. Set `ui.file-server-endpoint` to `0` for no file server. |
| `unix-socket` | `socket` | Fails when the directory does not exist, this user cannot write it and no socket is there, or the path is too long. Warns when the path is relative or holds a file that is not a socket, or this user cannot write the directory but another user's socket is there. | Set `unix-socket` to a path in a directory that this user can write. |
| `unix-socket-gid` | `socket` | Fails when phēnix does not run as root and this user is not in a group of that id, so `phenix ui` exits at startup. Warns when phēnix runs as root and no group has the id. | Set a group that this user is in, run `phenix ui` as root, or set `-1`. |
| `server` | `server` | Asks a running `phenix ui` for its version and modes. Warns when no `phenix ui` answers, or its version, `bridge-mode` or `deploy-mode` is different. Fails when this user cannot connect. | Start `phenix ui`, run the same phēnix version, or set the same modes. A stale socket file goes away when `phenix ui` starts. |
| `stuck-status` | `server` | Warns when the store shows app runs, SCORCH runs or delayed starts in progress, but no phēnix process runs them. This occurs when `phenix ui` stops during a start. | Start or restart `phenix ui`, which clears them. For an interrupted start, stop the experiment and start it again. |
| `ui-limits` (Linux only) | `server` | Warns when `phenix ui` uses more than 80% of its limit of open files. | Increase the limit with `LimitNOFILE=` in the systemd unit, or `docker run --ulimit nofile=<n>:<n>`. Then restart `phenix ui`. |
| `minimega` | `minimega` | Fails when minimega does not answer on its socket in `base-dir.minimega` within `--timeout`. The row names the likely cause. | Start minimega, or set `base-dir.minimega` to the directory of its socket. For a permission error, run doctor as the user that runs minimega, usually root. |
| `minimega-env` | `minimega` | Compares the flags of minimega with phēnix. Fails when phēnix cannot read the `-filepath` directory of minimega. Warns when `-filepath` is relative or not writable, `-base` is not `base-dir.minimega`, or the mesh fails. | Mount the files directory at the same path for phēnix (Docker Compose shares `/phenix`), and give this user access to it. Start minimega with an absolute `-filepath`. Set `base-dir.minimega` to the `-base` of minimega. |
| `minimega-limits` (Linux only) | `minimega` | Warns when the open files limit of minimega is too low for its VMs, or the host does not have sufficient memory and swap for the VMs. | Increase the limit of minimega, as for `ui-limits`. Stop VMs, or give them less memory. |
| `state-drift` | `minimega` | Warns when the store marks an experiment as running but minimega has no VMs for it, such as after minimega restarted. Also warns when minimega runs VMs that no experiment owns. | Reset the state with `phenix experiment stop <experiment>`. Kill the other VMs with `minimega -e 'clear namespace <namespace>'`. |
| `vlans` | `minimega` | Warns when two experiments pin the same VLAN ID, or a stopped experiment cannot get its VLANs now. | Pin another ID with `phenix vlan alias`, or set another range with `phenix vlan range`. Both commands need `--force`. |
| `bridge-trunk` (Linux only) | `minimega` | Warns when the mesh has more than one node and an experiment bridge has no trunk or tunnel port. VMs on different nodes then cannot connect. | Add a trunk on each node with `minimega -e 'bridge trunk <bridge> <interface>'`, or set `use-gre-mesh` to `true`. |
| `schedule-hosts` | `minimega` | Warns when an experiment schedules a VM on a node that is not in the minimega mesh. | Change the schedule with `phenix experiment edit <experiment>`, or connect the node to the mesh. |
| `cc-clients` | `minimega` | Warns when a running VM that needs miniccc has no client after 5 minutes, or the miniccc version of a client is not the minimega version. | Put the miniccc of the same minimega release in the image with `phenix image inject-miniexe <miniccc> <image>`, and start it when the VM boots. |
| `qemu-img`, `vmdb2` | `tools` | Fails when the program is not on `PATH`. phēnix needs `qemu-img` for disk images, and `vmdb2` for `phenix image build`. | Install the program (`qemu-utils` and `vmdb2` on Debian and Ubuntu). If you do not build images, use `--skip vmdb2`. |
| `filebeat` | `tools` | Warns when `filebeat` is not on `PATH`, so SCORCH starts no Filebeat. | Install Filebeat for the SCORCH components that use it. |
| `disk` | `tools` | Warns when a filesystem of phēnix or minimega is 90% full, and fails when it is 98% full. | Increase the free space on that filesystem. |
| `kernel-modules` (Linux only) | `tools` | Warns when the modules of the running kernel are missing, such as after a kernel upgrade, or when a reboot is pending. | Reboot the host into the installed kernel. |
| `nbd` (Linux only) | `tools` | Checks what minimega needs for injects. Fails when `qemu-nbd` is missing. Warns when the `nbd` module is not loaded or has no partitions, no `/dev/nbdN` device is free, or a failed inject left a device connected. | Load the module as root with `modprobe nbd max_part=10`. Disconnect a device that no inject uses with `qemu-nbd -d /dev/nbdN`. Run a container privileged, with `-v /dev:/dev`. |
| `openvswitch` (Linux only) | `tools` | Fails when `ovs-vsctl` is missing (a `skip` row in a container) or Open vSwitch does not answer. Warns when this user cannot connect to `ovsdb-server` (permission denied), `ovs-vswitchd` does not run, or the module is not loaded. | Install or start Open vSwitch (`openvswitch-switch` on Debian and Ubuntu). For permission denied, run doctor as root, as minimega runs. |
| `image-build` (Linux only) | `tools` | Warns when `phenix image build` does not have a program, root access, a loop device, or a debootstrap script or keyring that it needs. | Install the packages that the row names. Run `phenix image build` as root or in a privileged container. |
| `image-configs` | `tools` | Warns when an overlay directory of an image config does not exist, so `phenix image build` fails. | Create the directory, or remove it from the config with `phenix image remove <config> --overlays <path>`. |
| `disk-chains` | `images` | Warns when a disk image has a broken backing chain: a missing backing file, a loop, or a chain deeper than 64 images. | Restore the backing file, rebase the image (`Rebase` on the Disks page), or delete the image. |
| `disk-formats` | `images` | Warns when an image has an old or unsupported format, qemu refuses it, or the local `qemu-img` is too old for it. | Upgrade or convert the image. See [Image Format Versions](disks.md#image-format-versions). |
| `writable-images` | `images` | Warns when a VM with `snapshot: false` writes to an image that backs other images, or that more than one VM uses. | Set `snapshot: true`, or give each VM its own copy of the image. |
| `leftovers` | `images` | Warns about the snapshot files, directories, mounts, taps and mirrors that deleted experiments or stopped VMs left. | Delete or unmount each item that the row names. The hint gives the command. |
| `disk-space` | `images` | Warns when the filesystem of the images, the minimega `-base` or the minimega log has less than 10% or 5 GiB free. Also warns about the directories that a failed inject left. | Delete the images that you do not use and the leftovers, and rotate the minimega log. |
| `image-locks` (Linux only) | `images` | Warns when a qemu process holds open an image of a stopped experiment, so the next start fails with `Failed to get "write" lock`. | Stop the process when nothing uses it: `kill <pid>`, or `qemu-nbd -d /dev/nbdN`. |
| `inject-partitions` | `images` | Warns when the `inject_partition` of a VM names a partition that minimega cannot write files to, or a Windows or ext file system that needs a repair. The row suggests a partition. | Set `inject_partition` to the suggested partition, or to `0` for no injects. For a Windows volume, boot the image, run `chkdsk /f` if it is dirty, and shut down fully with Fast Startup and hibernation off (`powercfg /h off`). For ext, run `e2fsck -f`. Then commit or rebuild the image. See [Injects Go to the Wrong Partition](troubleshooting.md#injects-go-to-the-wrong-partition). |
| `apps` | `apps` | Lists the user apps and SCORCH components on `PATH`. It never warns. | No fix is necessary. |
| `scenario-apps` | `apps` | Warns when a scenario or experiment uses an app or SCORCH component that is not built in and not on `PATH`. | Install the app, or add its directory to `PATH` for the user that runs phēnix. |
| `app-runtimes` | `apps` | Warns when an app on `PATH` cannot run: it is not executable, its `#!` interpreter is missing, or it has Windows line ends. | Run `chmod +x <path>`, install the interpreter, or change the line ends with `dos2unix <path>`. |
| `tunneler-download`, `webshark` | `features` | Tells if `phenix ui` turns on the feature. `tunneler-download` warns when `downloads/tunneler` exists but is not a directory. `webshark` never warns. | Make `downloads/tunneler` a directory of `phenix-tunneler` builds, or remove it. |
| `ui.features` | `features` | Fails when `webshark` is set without a WebShark build. Warns about an unknown feature name, or a feature that does not have what it needs. | Keep only `vm-mount` in `ui.features`. `phenix ui` turns on the other features when it finds what they need. |
| `ui.logs.minimega-path` | `features` | Fails when the path is not a regular file. Warns when the file is missing or not readable, or the setting is empty while minimega logs to a file. | Set it to the file that minimega logs to, readable by this user. |
