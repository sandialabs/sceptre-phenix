# Settings & Configuration

phēnix supports dynamic configuration management via the `phenix settings` command.
A running `phenix ui` applies changes to the `log.*` settings, `ui.logs.level`,
`experiment.disk-check` and the users file at once, without restarting. It reads every other setting only at
startup. On each config file change it logs a warning that names every such key
that now differs from the value it started with (never the values themselves):

```text
config change takes effect only when phenix ui restarts keys="store.endpoint, ui.listen-endpoint"
```

## Configuration Files

phēnix looks for a configuration file (e.g. `config.yaml`, `config.json`, `config.toml`) in the following locations, in order, and reads the first it finds:

**When run as root:**

* `./config.yaml` (the working directory)
* `/etc/phenix/config.yaml`

**When run as a regular user:**

* `./config.yaml` (the working directory)
* `$HOME/.config/phenix/config.yaml`
* `/etc/phenix/config.yaml`

It looks for `users.yaml` in the same locations. `sudo phenix` searches as
the user that runs `sudo`, so it also searches that user's
`$HOME/.config/phenix`.

Before any command runs, phēnix creates the default log directory
(`$HOME/phenix_logs`, or `/var/log/phenix` as root) and, as root,
`/etc/phenix`. When it finds no config file, it also creates
`$HOME/.config/phenix/config.yaml` (`/etc/phenix/config.yaml` as root),
empty, so that it can watch the file for changes. `phenix doctor` lists every
config and users file it finds in its `config-paths` check.

### Example Configuration

```yaml
base-dir:
  minimega: /tmp/minimega
  phenix: /phenix
log:
  level: info
  console: stderr
  system:
    path: /var/log/phenix/phenix.log
store:
  endpoint: bolt:///etc/phenix/store.bdb
ui:
  listen-endpoint: 0.0.0.0:3000
  jwt-signing-key: abcde12345
  logs:
    minimega-path: /var/log/minimega/minimega.log
```

## Configuration vs. Data Store

It is important to distinguish between the **System Configuration** and the **Data Store**:

* **System Configuration (`config.yaml`)**: Configures the phēnix *daemon* itself. It controls *how* the service runs (e.g., log levels, API ports, storage backend connection strings).
* **Data Store (BoltDB)**: Stores the *application state*. It holds *what* phēnix manages (e.g., defined experiments, topologies, user accounts, active VM states).

Deleting `config.yaml` resets daemon behavior to defaults. Deleting the Data Store results in data loss (experiments, users, etc.).

## Store Configuration

The phenix tool uses a bbolt key-value data store, a single file with no external dependencies that is only accessible on one machine. Set its path with the `bolt://` store endpoint (e.g. `bolt:///etc/phenix/store.bdb`).

## Managing Settings

```bash
# View all current runtime settings
phenix settings list

# Change log level to debug
phenix settings set log.level debug

# Revert a setting to default
phenix settings unset log.level

# Reset all settings
phenix settings unset --all
```

## Checking the Configuration

`phenix doctor` checks the configuration without a running `phenix ui`, so it
helps most when the server will not start or a setting seems to have no
effect:

```bash
docker exec phenix phenix doctor              # Docker deployment
phenix doctor -o json --timeout 10s           # JSON, waiting longer for minimega
phenix doctor --skip minimega -x --strict     # no minimega check or settings list; fail on a warning
phenix doctor -p -x                           # only the checks that warn or fail
docker exec phenix phenix doctor --report     # output to paste into a bug report
```

It prints, in order:

1. A header: the phēnix version, commit and build date, minimega's version
   when the `minimega` check answers, the OS and kernel, the user with its
   uid and gid, the working directory, the config file read, whether phēnix
   runs in a container (from `/.dockerenv`, `/run/.containerenv` or
   `/proc/1/cgroup`), and the time.
2. Every setting with its effective value and its source: `flag`, `env`,
   `config file` or `default`. `-x` (`--no-print-settings`) leaves it out,
   and empties the `settings` list of the JSON report.
3. One row per check, with its status: `pass`, `warn`, `fail` or `skip`.
   `-p` (`--problems-only`) shows only the rows that warn or fail.
4. A summary line with the overall status, the count of each status and the
   names of the checks that failed or warned, then a hint for each failure.
   With `--strict`, warnings get hints too. With `-p`, the summary still
   counts every check and gives the number of hidden checks. The exit
   status does not change.

On a terminal, doctor shows `pass` in green, `warn` in yellow, `fail` in
bold red and `skip` faint. The summary line has the color of the overall
status, and the hints are faint. `--color` sets when doctor uses colors (see
[Colors](#colors)). `-o json` and `--report` never have color.

`--skip` takes a check or a group from the table below, comma-separated or
repeated, such as `--skip minimega` or `--skip env,ui.jwt-signing-key`. A
skipped check shows as a `skip` row. An unknown name fails and lists the
valid ones.

| Check | Group | Warns when | Fails when |
| :--- | :--- | :--- | :--- |
| `config-file`, `users-file` | `config` | no `config.yaml` is found | the file does not parse or cannot be read, so phēnix ignores it; for a parse error the row gives the line, never the file's content |
| `config-keys` | `config` | the config files hold keys that are not phēnix settings, such as a misspelling | never |
| `config-paths` | `config` | phēnix ignores a config or users file on the search path, since it reads only the first of each name; the row lists the directories searched and each file found | never |
| `settings-file` | `config` | `phenix settings set` would write a file other than the config file phēnix reads, a file that is not YAML, or a file this user cannot write or create | never |
| `env` (one row per set variable, named after it) | `env` | a `PHENIX_*` variable is empty, a flag overrides it, phēnix reads it as a value other than the one written (such as `010` as `8`), it holds a log level phēnix ignores, or it names no setting, such as a misspelling; `PATH` has relative entries; `SUDO_USER` names no user when root runs phēnix through `sudo` | phēnix would refuse or misread a `PHENIX_*` value; `EDITOR` is not a program on `PATH` |
| `env-overrides` | `env` | a `PHENIX_*` variable overrides a different value in the config or users file; the row names the variable, the setting and the file, never a value | never |
| `bridge-mode`, `deploy-mode`, `experiment.disk-check`, `color`, `log.level`, `log.system.max-size`, `log.system.max-backups`, `log.system.max-age` | `settings` | `log.level` is not a level phēnix knows, so it logs at `info`; a `log.system.*` value is not a whole number, so phēnix reads `0` (100 MB for `max-size`, no limit for the others); `max-backups` or `max-age` is negative | `bridge-mode`, `deploy-mode`, `experiment.disk-check` or `color` is a mode every phēnix command refuses; `log.system.max-size` is negative, so every write to the system log fails |
| `store.endpoint` | `files` | never | the file cannot be written, or its directory does not exist or cannot be written |
| `store-file` (only when the store file exists) | `files` | one of its two meta pages is corrupt, so BoltDB reads the other, which may not hold the last write | the file is not a BoltDB file, has another format version or byte order, is shorter than its two meta pages, or both meta pages are corrupt |
| `store-owner` (only when the store file exists) | `files` | another user owns the file and this user is not root; as root under `sudo phenix`, the file does not belong to the user whose store `sudo phenix` uses | never |
| `store-root` (only for the default store of a user other than root) | `files` | `/etc/phenix/store.bdb` exists, or this user cannot see whether it exists, so a `phenix ui` that runs as root uses a different store | never |
| `log.system.path`, `log.console` | `files` | never | the file (`phenix.log` in it when `log.system.path` is a directory) cannot be written, or created in the nearest directory above it that exists; `log.console` gets a row only when it names a file |
| `base-dir.phenix`, `base-dir.minimega`, `base-dir.injects`, `base-dir.topologies`, `mount-dir` | `dirs` | the directory does not exist, is not readable and searchable by this user, is relative, or is one phēnix writes to and is not writable by this user | the path is not a directory |
| `ui.shutdown-grace-period`, `ui.netflow.max-size` | `ui` | never | `phenix ui` would refuse the value |
| `ui.jwt-signing-key` | `auth` | the key is empty (no sign-in) or a development key | a development key does not name exactly a user and a role, so `phenix ui` refuses every request |
| `ui.jwt-lifetime` | `auth` | never | phēnix reads the value as under a minute: `24` is 24 nanoseconds, and a value that is not a duration, such as `1d`, is 0 |
| `ui.users` | `auth` | an entry's role is not built in, so `phenix ui` skips the user unless the store holds the role; the list is empty while sign-in is on, so `phenix ui` creates the default user `admin@foo.com` | an entry is not `name:password:role[:resource...]`, which `phenix ui` skips without an error, or its role holds a comma; a row names an entry by position and user name, never by its password |
| `proxy-jwt` (only with the `proxy-jwt` key) | `auth` | `ui.listen-endpoint` listens on every address, and `phenix ui` does not check the signatures of proxy JWTs | never |
| `ui.proxy-auth-header` (only when set) | `auth` | the JWT signing key is empty or a development key, so `phenix ui` ignores the header | the value is not an HTTP header name |
| `ui.tls` | `tls` | the certificate expires within 30 days | only one of `ui.tls-key` and `ui.tls-cert` is set, so `phenix ui` serves plain HTTP; a file cannot be read; the key and certificate do not load as a pair; the certificate has expired or is not valid yet |
| `ui.listen-endpoint`, `ui.file-server-endpoint` | `tls` | the listen endpoint has no port, so `phenix ui` listens on a random one | `phenix ui` cannot listen on the value or refuses it |
| `unix-socket` | `socket` | the path is relative, or holds a file that is not a socket, which `phenix ui` deletes; this user cannot write the directory, but a socket is there, such as one a `phenix ui` of another user serves | the directory does not exist, this user cannot write it and no socket is there, or the path is too long for a unix socket |
| `unix-socket-gid` | `socket` | phēnix runs as root and no group has the id | phēnix does not run as root, and this user is not in a group of that id, so `phenix ui` exits when it sets the socket's group |
| `server` (with the rows server bridge-mode and server deploy-mode when phenix ui answers) | `server` | no `phenix ui` answers on `unix-socket`: no socket file, a stale one, a file that is not a socket, or no answer within `--timeout`; the version of `phenix ui` is not the version of this phēnix; its `bridge-mode` or `deploy-mode` is not the one set here | this user may not connect to the socket |
| `minimega` | `minimega` | never | minimega does not give its version on `<base-dir.minimega>/minimega` within `--timeout` (3 seconds by default); the row names the likely cause: no socket, a stale one, or a user that may not connect |
| `minimega-env` (the rows minimega files, minimega base and minimega mesh; one skip row when minimega gives no version) | `minimega` | minimega gives no flags (it is older than 3.0); its `-filepath` is relative or not writable by this user; its `-base` is not `base-dir.minimega`; `mesh status` fails when `-degree` is above 0 | the `-filepath` directory does not exist for phēnix or is not readable and searchable by this user |
| `minimega-limits` (the rows minimega-limits open files and minimega-limits memory; Linux only) | `minimega` | the open files row: the soft limit of open files of the minimega process is lower than its VMs on this node need. The estimate is 64 files, and 5 for each VM (QMP, VNC, cc and two qemu output pipes). The row also warns when more than 80% of the limit are open. The memory row: `MemAvailable` is less than 10% of `MemTotal`, more than 50% of the swap is in use, or the VMs on this node ask for more memory than the memory and swap together. doctor finds minimega through `minimega.pid` in its `-base`. A `skip` row shows when this user cannot read `/proc/<pid>`, or when minimega runs in another container | never |
| `state-drift` (the rows state-drift experiments and state-drift namespaces) | `minimega` | the experiments row: the store marks an experiment as running, but minimega has no namespace or no VMs for it, such as after minimega restarted. The namespaces row: a minimega namespace has VMs, but no stored experiment has its name. Both rows are `skip` rows when doctor cannot read the store or minimega | never |
| `vlans` (the rows vlans pinned and vlans free) | `minimega` | the pinned row: two stored experiments pin the same VLAN ID in `vlans.aliases`, so they cannot run at the same time. The free row: a stopped experiment cannot get its VLANs now. Its range (`vlans.min` and `vlans.max`, else the range of minimega) has fewer free VLANs than the experiment needs. Or its range overlaps the range of another namespace, or another namespace uses a VLAN that it pins | never |
| `bridge-trunk` (Linux only) | `minimega` | minimega's mesh has more than one node, and an experiment bridge on this node has no trunk or tunnel port. An experiment bridge is `phenix`, or a bridge with VM taps. A trunk is an untagged port of a physical NIC, a bond, a VLAN device or a tunnel, such as the port that minimega `bridge trunk` adds. With one node, the row is the `skip` row "one node, no trunk needed". With `use-gre-mesh` on, the row passes | never |
| `schedule-hosts` | `minimega` | the VM schedules (`schedules`) of a stored experiment name a cluster node that is not in minimega's mesh (`mesh list all`). The row compares the names without the `hostname-suffixes` | never |
| `cc-clients` | `minimega` | a VM of a running experiment has run for 5 minutes or more, phēnix needs a miniccc client in it, and minimega's `cc clients` has none for it. phēnix needs a client when the startup app sends its scripts through miniccc, when another VM waits for the client at the start (`delay.c2`), or when the `soh` app checks the VM. The row also warns when the last MiB of minimega's `-logfile` shows a miniccc version that differs from the minimega version for a client, in a line logged after the VM of that client launched. doctor does not read a relative `-logfile` | never |
| `qemu-img`, `vmdb2` | `tools` | never | the program is not on `PATH`, or `qemu-img --version` fails: phēnix commits VMs to disk images with `qemu-img`, and `phenix image build` runs `vmdb2` |
| `filebeat` | `tools` | the program is not on `PATH`, so SCORCH starts no Filebeat, even for a component that enables it | never |
| `apps` | `tools` | never | never; the row lists the user apps (`phenix-app-*`) and SCORCH components (`phenix-scorch-component-*`) found on `PATH` |
| `disk` (one row per filesystem, such as disk phenix-base) | `tools` | the filesystem of `base-dir.phenix`, the store or minimega's `-filepath` is 90% full | the filesystem is 98% full |
| `kernel-modules` (Linux only) | `tools` | `/lib/modules/<kernel release>` does not exist, so `modprobe` cannot load modules such as `nbd` and `openvswitch`; `/var/run/reboot-required` exists. A container without `/lib/modules` gives a `skip` row | never |
| `nbd` (Linux only) | `tools` | the `nbd` module is not loaded, is part of the kernel (so `lsmod` does not show it), or has `max_part` missing or 0; no `/dev/nbdN` device is free, or a container has no `/dev/nbd*` device; a device has a lock file in `/var/lock` and no connection, belongs to a process that is not running, or holds an image for a `qemu-nbd` that a failed inject can leave, with its mounted partition. minimega `disk inject` uses these devices for injects at a start and for `phenix image inject-miniexe`. A row for a missing module points to `kernel-modules` | `qemu-nbd` is not on `PATH` |
| `openvswitch` (Linux only) | `tools` | this user cannot connect to `ovsdb-server` (permission denied), but minimega runs `ovs-vsctl` as root; `ovs-vswitchd` does not run; the `openvswitch` module is not loaded, and the row points to `kernel-modules`. The row gives the Open vSwitch version, and tells if the bridge `phenix` exists or if minimega makes it at the first VM launch. In a container without `ovs-vsctl`, the row is `skip` | `ovs-vsctl` is not on `PATH`; `ovs-vsctl show` gets no answer from `ovsdb-server` within `--timeout` |
| `image-build` (the rows image-build tools, image-build root, image-build loop and image-build keyrings; Linux only) | `tools` | `debootstrap`, `kpartx`, `losetup` or `qemu-img` is not on `PATH`; the process does not have `CAP_SYS_ADMIN` (not root, or a container that is not privileged); no `/dev/loop*` device is free and there is no `/dev/loop-control`; debootstrap has no script for the release of a stored image config, or the keyring that the script names does not exist. The keyrings row reads the image configs from the store read-only, and gives `skip` when the store is locked for longer than `--timeout` | never |
| `disk-chains` | `images` | a disk image in the images directory has a broken backing chain: a backing file is missing, the chain loops, or the chain is more than 64 images deep; the row names at most 5 such images and counts the rest; the directory cannot be listed; the check does not finish within `--timeout` | never; the images directory is minimega's `-filepath`, or `<base-dir.phenix>/images` when minimega gives none, and a `skip` row shows when it does not exist |
| `disk-formats` (a second row when an image needs a later qemu) | `images` | a disk image in the images directory is in an old or unsupported format: qcow2 version 2, qcow version 1, vmdk or qed. It also warns about an image that qemu refuses, such as an encrypted or corrupt image, and when the local `qemu-img` is older than the qemu version that a feature of an image needs. The row names at most 5 such images and counts the rest. It also warns when it cannot list the directory, or when the check does not finish within `--timeout`. See [Image Format Versions](disks.md#image-format-versions) | never. It reads the headers that the `disk-chains` check reads, in the same pass over the images directory, and a `skip` row shows when that directory does not exist |
| `writable-images` | `images` | a VM of a stored experiment has `snapshot: false`, so minimega writes to its drive images. The row warns when another image in the images directory has such an image as a backing file, because the writes break that image. It also warns when more than one VM uses such an image. It reads the experiments from the store, and the backing files from the pass of `disk-chains` | never. A `skip` row shows when the store cannot be read, or when the store file does not exist |
| `leftovers` (the rows leftovers snapshots, leftovers files, leftovers mounts, leftovers taps and leftovers mirrors) | `images` | the snapshots row: a snapshot file of this head node in the images directory (`<head node>_<experiment>_<VM>_snapshot`) belongs to an experiment that the store does not have. The files row: a base directory in `<base-dir.phenix>/experiments`, or a directory with `files` or `miniccc_responses` in the images directory, belongs to such an experiment. The row gives each size and the total. The mounts row: a VM file system mount under `mount-dir` belongs to such an experiment, or to a VM that minimega does not run. The taps row: a minimega tap (`mega_tap*`) on this host or on an Open vSwitch bridge has no owner, or a port has no network device. An owner is a VM or a host tap in a minimega namespace, or a capture of a whole bridge. The mirrors row: a minimega mirror (`mirror-<tap>`) belongs to no capture. The rows keep what belongs to a namespace in which minimega runs a VM. In a container, the taps row checks the taps of this host only when it shows a minimega tap, an Open vSwitch bridge or the `ovs-system` device. A row that cannot get its data from the store, minimega or `ovs-vsctl` is a `skip` row with the reason. A store file that does not exist gives a `skip` row, because `phenix doctor` can read a store other than the store of `phenix ui` | never |
| `disk-space` (one row for each filesystem, such as disk-space files, and the row disk-space inject dirs) | `images` | the filesystem of the images directory, of minimega's `-base` or of minimega's `-logfile` has less free space than 10% of its size or 5 GiB, whichever is larger. The row names each filesystem once. It gives the size of the minimega log, and the size of the files that the `leftovers` rows found. The inject dirs row warns about each `dstImg*` directory in minimega's `-base`, which a failed `disk inject` can leave, and tells which ones are mounted | never |
| `image-locks` (Linux only) | `images` | a `qemu-system`, `qemu-nbd` or `qemu-img` process holds open a drive image of a stopped experiment, or one of its backing files, and is not a VM of a running experiment. qemu then cannot get the lock it needs to start the VM (`Failed to get "write" lock`). Any open of an image that a VM writes to (`snapshot: false`) blocks the start. An open of another image blocks it only when the process can write to the file. The row tells how many processes this user cannot read | never. A `skip` row shows in a container that shows no minimega process, because the container can have its own PID namespace |
| `tunneler-download`, `webshark` | `features` | `downloads/tunneler` is not a directory | never; the row says whether `phenix ui` turns the feature on |
| `ui.features` | `features` | a name is not a feature, such as a misspelling; `tunneler-download` or `file-server` is set without what it needs | `webshark` is set without a WebShark build, which `phenix ui` refuses |
| `ui.logs.minimega-path` | `features` | the file does not exist or is not readable by this user; the setting is empty while minimega logs to a file (its `-logfile`, or `MM_LOGFILE` in the environment) | the path is not a regular file |

Every phēnix command reads the `settings` group before it runs, and `phenix
doctor` skips that step, so these checks show what the other commands would
refuse. They check the value phēnix uses, from wherever it comes, by the
rules of the `env` rows.

The `env` check covers each environment variable phēnix reads, `PHENIX_*`,
`PATH`, `EDITOR`, `SUDO_USER` and `SUDO_COMMAND`, with a row only when the
variable is set. It checks each the way the code that reads it parses it:
whole numbers, `true` or `false`, log levels, modes, durations with a unit (a
bare `24` in `PHENIX_UI_JWT_LIFETIME` is 24 nanoseconds), `host:port`
endpoints, header names and paths. A directory variable follows the rules of
the `dirs` rows above, a file variable those of the `files` rows, and
`PHENIX_UI_TLS_KEY` and `PHENIX_UI_TLS_CERT` must name readable files.
`PHENIX_UI_BASE_PATH` shows the path `phenix ui` serves under, with a leading
and a trailing `/` added when missing. `PHENIX_UI_FEATURES` fails when it
holds a comma. `PHENIX_UI_USERS`, `PHENIX_UI_JWT_SIGNING_KEY`,
`PHENIX_UI_JWT_LIFETIME`, `PHENIX_UNIX_SOCKET` and `PHENIX_UNIX_SOCKET_GID`
follow the rules of their `auth` and `socket` rows.

The `config-paths` check names the file phēnix reads, marked `(read)`, and
each one it ignores. In each directory phēnix tries the extensions in the
order `json`, `toml`, `yaml`, `yml`, then less common ones, so a
`config.json` hides a `config.yaml` beside it. Since phēnix creates an empty
config file when it finds none, `config-file` warns only when it cannot. The
`settings-file` check names the file `phenix settings set` and `unset`
change, which is the file phēnix reads.

The store checks read the first two pages of the store file with a plain
read, never through BoltDB, which locks the file. A BoltDB file starts with
two meta pages, each with the magic number `0xED0CDAED`, the format version
`2`, the page size and a checksum. phēnix creates the store readable and
writable by its owner only. A user other than root uses
`$HOME/.phenix.bdb` by default and root uses `/etc/phenix/store.bdb`, so a
command run without root sees different experiments than a `phenix ui` that
runs as root. `sudo phenix` uses the store of the user that runs `sudo`, so
a store it creates belongs to root but is in that user's home directory.

The `auth`, `tls` and `socket` groups check what `phenix ui` reads at startup.
A development key is `dev|<user>|<role>`. Since doctor opens no store, it
checks the roles of `ui.users` and of a development key against the built-in
roles only, and warns about any other. The `tls` checks read the key and
certificate files, and print neither.

The `disk-chains` check reads only the image headers, as the disk check before
an experiment start does (see
[Disk Check Before a Start](experiments.md#disk-check-before-a-start)). It
never runs `qemu-img` and sends minimega no command. It checks only the files
on this host. When it runs out of time, it warns and gives the number of
images it checked. The `disk-chains` and `disk-formats` checks read each file
in the images directory that the [Disks](disks.md) page lists by its extension:
`.qcow2`, `.qc2`, `.qcow`, `_rootfs.tgz`, `.hdd`, or `.iso`.

`-o json` prints the same report as one JSON object: `schemaVersion` (`1`),
`status`, `time`, `version` (`phenix`, `label`, `commit`, `date`,
`minimega`), `environment`, `checks` (each with a stable `id`, its `group`,
`name`, `status`, `detail` and, when it warns or fails, `hint`) and
`settings`. With `-p`, `checks` holds only the checks that warn or fail,
`hiddenChecks` gives the number of the others, and `status` is still the
status of every check.

The `server` check asks `phenix ui` on `unix-socket` for `GET /api/v1/options`,
as every other phēnix command does before it runs, and reads the
`X-Phenix-Version` header. A phēnix command uses `auto` when `phenix ui` uses
`bridge-mode: auto`, whatever is set here, and the `bridge-mode` set here
otherwise. It uses the `deploy-mode` set here, or the mode of `phenix ui`
when none is set. The mode rows name the mode the command uses. A failure to
connect warns, since `phenix doctor` also runs while `phenix ui` is stopped.

The `minimega-env` check sends minimega its `args` command and reads the
flags minimega runs with. phēnix resolves relative image paths against the
`-filepath` directory and keeps disk images in it, so phēnix must see it at
the same path, as the Docker Compose setup does with `/phenix`. The
`minimega mesh` row gives `-degree` and `-abssnapshot` (see
[Disks](disks.md)) and, when `-degree` is above 0, the size of the mesh.

The `tools` and `features` groups check what parts of phēnix need beyond
its settings. `phenix ui` turns on `tunneler-download` when the directory it
starts in holds `downloads/tunneler`, `webshark` when `webshark/index.html`
is there or in `/opt/phenix/webshark`, and `file-server` when
`ui.file-server-endpoint` is set. The rows name the working directory of
`phenix doctor`, so run it in the directory `phenix ui` starts in, such as
`/opt/phenix` in the Docker image. When `ui.logs.minimega-path` names a file
that is not there, `phenix ui` waits for it without a warning, and the Logs
page shows no minimega logs.

`--report` prints the whole output, text or JSON, in a fenced code block to
paste into the
[bug report form](https://github.com/sandialabs/sceptre-phenix/issues/new?template=1-bug-report.yml).
It removes data about the host from every line: the header, the settings,
the check details and the hints.

* It replaces host names, IP addresses, user names and email addresses with
  tokens, such as `<host-1>`, `<ip-1>`, `<user-1>` and `<email-1>`. The same
  value always gets the same token, and a path keeps its shape, such as
  `/home/<user-1>/images`.
* It replaces passwords, the JWT signing key, other secret-like values (such
  as `password=...` or `token=...`) and the credentials in a URL with
  `<redacted>`.
* It keeps `localhost`, `127.0.0.1`, `::1`, `0.0.0.0` and `::`, the users
  `root`, `phenix`, `minimega`, `nobody` and `admin@foo.com`, and a suffix
  from `hostname-suffixes`: `ws42-phenix` becomes `<host-1>-phenix`.

doctor finds the values to replace in the host name, the host name that
minimega answers from, the nodes of its mesh, the nodes that VM schedules name, the user and its home directory, and `SUDO_USER`. It also reads the `ui.users` entries, the JWT
signing key, the interface addresses and the file owners that the checks name.
doctor also replaces each IPv4 and IPv6 address and each email address it
finds. Read the output before you paste it: doctor cannot find a name that it
does not know, such as the name of another host in a path. doctor never prints the hostname, also without
`--report`.

`phenix doctor` exits 1 when a check fails, or with `--strict` when one warns,
and 0 otherwise. It never prints the values of `ui.jwt-signing-key` or
`ui.users`, only `<redacted>` when they are set, nor those of
`PHENIX_UI_JWT_SIGNING_KEY`, `PHENIX_UI_USERS`, `SUDO_COMMAND` or a
`PHENIX_*` variable that names no setting. The `store-file` check reads only
the first two pages of the store file. The `image-build`, `writable-images`,
`leftovers` and `image-locks` checks open the store through BoltDB, once in a
run. doctor opens the store read-only, and waits at most `--timeout` for the
lock. The unix socket of `phenix ui` serves no configs, so doctor does not ask
`phenix ui` for them. It opens no log
file, writes no file and binds no port. It connects to the unix socket of
`phenix ui` as a client only, and never creates or removes it. It sends
minimega only commands that change nothing: `version`, `args`, `mesh status`
and `namespace`. For each namespace that minimega lists, it also sends
`namespace <name> vm info`, `namespace <name> tap` and
`namespace <name> capture`. The only programs it runs are `qemu-img --version`,
`ovs-vsctl show`, `ovs-vsctl list Mirror` and `ovs-vsctl list Bridge`, which
change nothing. Run it with the same flags, environment and user as the command you
are checking, since those decide the values it sees.

## Configuration Precedence

phēnix resolves each setting in the following order (highest to lowest):

1. **Command Line Flags**: Arguments passed directly to the binary (e.g., `phenix ui --log.level=debug`).
2. **Environment Variables**: Variables like `PHENIX_LOG_LEVEL`. An empty variable is ignored.
3. **Config File**: Settings in `config.yaml` (managed via `phenix settings set`), with
   `users.yaml` merged over it.
4. **Defaults**: Internal application defaults.

A `PHENIX_*` variable therefore hides a value that `phenix settings set` writes
to the config file. `phenix doctor` warns about each one in its `env-overrides`
check; unset the variable to use the file's value.

`phenix settings set` and `phenix settings unset` change the config file
phēnix read, the first one found in the locations above. When phēnix read
none, `set` creates `config.yaml` in `$HOME/.config/phenix/`, or
`/etc/phenix/` as root. Both refuse a config file that is not YAML.

## Global Flags

The following global flags are supported by all `phenix` subcommands. The
[CLI reference](cli/index.md) lists them with every command's own flags.

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--base-dir.injects` | `string` | `<base-dir.phenix>/injects` | Directory that `phenix workflow apply` stages topology injects into. The `phenix ui` server reports its value to the CLI, and the flag on the `phenix workflow apply` command line overrides it. See [Workflow](workflow.md). |
| `--base-dir.minimega` | `string` | `/tmp/minimega` | Base minimega directory. |
| `--base-dir.phenix` | `string` | `/phenix` | Base phēnix directory. |
| `--base-dir.topologies` | `string` | `<base-dir.phenix>/topologies` | Directory that `phenix workflow apply` looks up topology directory names in. See [Workflow](workflow.md). |
| `--bridge-mode` | `string` | `manual` | Bridge naming mode for experiments (`auto` uses experiment name for bridge; `manual` uses user-specified bridge name, or `phenix` if omitted). A `phenix ui` running in `auto` mode makes the commands that reach it over `--unix-socket` use `auto`. See [Bridge Mode](bridge-mode.md). |
| `--color` | `string` | `auto` | When to color the output (`auto`, `on`, or `off`). `auto` colors only a terminal, and not when `NO_COLOR` is set or `TERM` is `dumb`. See [Colors](#colors). |
| `--deploy-mode` | `string` | — | Deploy mode for minimega VMs (`all`, `no-headnode`, or `only-headnode`). |
| `--experiment.disk-check` | `string` | `error` | What a start does when a VM disk image or CD-ROM image is missing, or its backing chain is broken (`error`, `warn`, or `off`). See [Disk Check Before a Start](experiments.md#disk-check-before-a-start). |
| `-h, --help` | — | — | Display help information for the command. |
| `--hostname-suffixes` | `string` | `-minimega,-phenix` | Hostname suffixes to strip. |
| `--log.console` | `string` | `stderr` | Destination output for console logs in text format (`stderr`, `stdout`, or a file path). |
| `--log.level` | `string` | `info` | Level to log messages at (`debug`, `info`, `warn`, `error`). |
| `--log.system.max-age` | `int` | `90` | Maximum number of days to retain old log files. |
| `--log.system.max-backups` | `int` | `3` | Maximum number of old log files to retain. |
| `--log.system.max-size` | `int` | `100` | Maximum size in megabytes of the system log file before rotation. |
| `--log.system.path` | `string` | `/var/log/phenix/phenix.log` | Path to persistent system log in JSON Lines format, with one JSON object per line. |
| `--mount-dir` | `string` | `<base-dir.phenix>/mounts` | Base directory for VM filesystem mounts. |
| `--store.endpoint` | `string` | `bolt:///etc/phenix/store.bdb` | BoltDB store endpoint (`bolt://<path>`). |
| `--unix-socket` | `string` | `/tmp/phenix.sock` | phēnix unix socket to listen on (`ui` subcommand) or connect to. |
| `--use-gre-mesh` | `bool` | `false` | Use GRE tunnels between mesh nodes for VLAN trunking. See [GRE Mesh](gre-mesh.md). |

## Colors

phēnix colors parts of its text output. The `--color` flag, the `color`
setting or `PHENIX_COLOR` sets when:

* `auto` (the default) colors an output only when it is a terminal. It
  does not color when `NO_COLOR` is set to a value or `TERM` is `dumb`.
* `on` colors every output, also a file or a pipe.
* `off` colors no output.

phēnix refuses another value. JSON and YAML output, and the output of
`phenix doctor --report`, never have color. A table keeps its columns
aligned, because phēnix measures each cell without its color codes.

| Output | Colors |
| :--- | :--- |
| Every command | The `Error:` label of a failed command is red. A console log record shows its level in color, the message of a warning in yellow, and the message of an error in red. A config file warning shows `warning:` in yellow. |
| `phenix doctor` | `PASS` green, `WARN` yellow, `FAIL` bold red, `SKIP` faint. The summary line has the color of the overall status. The hints are faint. |
| `phenix workflow apply`, also with `--dry-run` | The `action` of each config and of the plan: green for a create, yellow for an update or a restart, gray for no change. A disk problem that makes the start fail (`the start would fail: ...`) is red. Other warnings are yellow. |
| `phenix experiment list` | The `Started` time of a running experiment is green, and `(interrupted)` is yellow. |
| `phenix vm info` | The `Running` value is green for a running VM, red for a VM that minimega shows as `QUIT` or `ERROR`, and faint for other VMs. |
| `phenix job list`, `get` and `wait` | Job and item states: `running` cyan, `succeeded` green, `partial` and `canceled` yellow, `failed` red, and `queued`, `pending` and `skipped` faint. |

## Settings Reference

| Setting Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `bridge-mode` | `PHENIX_BRIDGE_MODE` | `manual` | Bridge naming mode for experiments. `auto` uses the experiment name as the bridge name; `manual` uses the user-specified bridge name, or `phenix` if not specified. See [Bridge Mode](bridge-mode.md). |
| `experiment.disk-check` | `PHENIX_EXPERIMENT_DISK_CHECK` | `error` | What a start does when a VM disk image or CD-ROM image is missing, or its backing chain is broken. `error` refuses the start, `warn` starts the experiment and shows each problem as a warning, and `off` does not check the images. It applies to every start: the CLI, the web UI, the REST API, `phenix workflow apply`, and the bulk start and restart of VMs. phēnix refuses another value. `phenix ui` applies a change at once. See [Disk Check Before a Start](experiments.md#disk-check-before-a-start). |
| `use-gre-mesh` | `PHENIX_USE_GRE_MESH` | `false` | Enable Layer 2 GRE tunnels for the default bridge of experiments created or updated with this setting. See [GRE Mesh](gre-mesh.md) for usage, server inheritance, and MTU requirements. `phenix ui` reads this setting at startup. **(Restart Required)** |
| `log.level` | `PHENIX_LOG_LEVEL` | `info` | Global log verbosity (`debug`, `info`, `warn`, `error`). |
| `color` | `PHENIX_COLOR` | `auto` | When phēnix colors its output: statuses in tables, `phenix doctor` rows, warnings and errors. `auto` colors only a terminal, and not when `NO_COLOR` is set or `TERM` is `dumb`. `on` and `off` ignore both variables. JSON and YAML output never have color. phēnix refuses another value. See [Colors](#colors). |
| `log.console` | `PHENIX_LOG_CONSOLE` | `stderr` | Destination for console logs (`stderr`, `stdout`, or a file path). Uses **Text/Human-Readable** format. Note: Setting this to a file path will prevent console logs from appearing in `docker logs`. |
| `log.system.path` | `PHENIX_LOG_SYSTEM_PATH` | `/var/log/phenix/phenix.log` | Path to the persistent system log file (used by UI). Uses **JSON** format. This is independent of `log.console` and is always active. |
| `log.system.max-size` | `PHENIX_LOG_SYSTEM_MAX_SIZE` | `100` | Max size in MB before rotation. |
| `log.system.max-backups` | `PHENIX_LOG_SYSTEM_MAX_BACKUPS` | `3` | Number of old log files to retain. |
| `log.system.max-age` | `PHENIX_LOG_SYSTEM_MAX_AGE` | `90` | Max age in days to retain old logs. |
| `ui.logs.level` | `PHENIX_UI_LOGS_LEVEL` | `""` | Log level for the web UI stream (defaults to `log.level`). |
| `ui.logs.minimega-path` | `PHENIX_UI_LOGS_MINIMEGA_PATH` | `""` | Path to the minimega log file to display in the UI and offer for download on the Logs page. **(Restart Required)** |
| `ui.features` | `PHENIX_UI_FEATURES` | `""` | Comma-separated list of optional UI features to enable (space-separated in `PHENIX_UI_FEATURES`). Currently supports `vm-mount`, which enables transferring files to and from a running VM. See [Mount a VM](vms.md#mount-a-vm). `phenix ui` turns on `webshark`, `tunneler-download` and `file-server` itself, and refuses to start with `webshark` set but no WebShark build. **(Restart Required)** |
| `ui.file-server-endpoint` | `PHENIX_UI_FILE_SERVER_ENDPOINT` | `0` (disabled) | Address (`port` or `host:port`) for the separate experiment file-upload server. A port-only value binds to `127.0.0.1`. Also works with the `vm-mount` feature - see [Uploading Experiment Files from the phēnix Server](vms.md#uploading-experiment-files-from-the-phenix-server). If it is set and the address cannot be listened on, `phenix ui` exits with an error. **(Restart Required)** |
| `ui.shutdown-grace-period` | `PHENIX_UI_SHUTDOWN_GRACE_PERIOD` | `5` | How many seconds stopping `phenix ui` may take to finish requests and jobs and close connections, as a whole number such as `10`. `0` closes everything without waiting; a negative value, or one that is not a whole number of seconds (such as `10s` or `1.5`), is refused at startup. Also `phenix ui --shutdown-grace-period`. See [Stopping the Server](#stopping-the-server). **(Restart Required)** |
| `ui.netflow.max-size` | `PHENIX_UI_NETFLOW_MAX_SIZE` | `100` | Maximum size in megabytes of each recorded netflow capture, as a whole number. A capture that reaches it stops recording and is marked truncated, while the live view goes on. `0` does not record captures; a negative value, or one that is not a whole number, is refused at startup. Also `phenix ui --netflow.max-size`. See [Recorded Captures](netflow.md#recorded-captures). **(Restart Required)** |
| `mount-dir` | `PHENIX_MOUNT_DIR` | `<base-dir.phenix>/mounts` | Base directory used for VM filesystem mounts created via `phenix vm mount`. See [Mount a VM](vms.md#mount-a-vm). |
| `base-dir.injects` | `PHENIX_BASE_DIR_INJECTS` | `<base-dir.phenix>/injects` | Directory that `phenix workflow apply` stages a topology directory's `phenix-injects/` into, as `<base-dir.injects>/<name>`. The phēnix server's value is used unless `--base-dir.injects` is given; see [Workflow](workflow.md#settings). Use an absolute path. **(Restart Required)** |
| `base-dir.topologies` | `PHENIX_BASE_DIR_TOPOLOGIES` | `<base-dir.phenix>/topologies` | Directory where `phenix workflow apply NAME` looks for `NAME` when `NAME` isn't an existing path. Use an absolute path. See [Workflow](workflow.md). |

## Stopping the Server

`phenix ui` shuts down cleanly on `SIGINT` (CTRL+C) or `SIGTERM` (`docker stop`),
taking at most `ui.shutdown-grace-period` seconds (5 by default):

* It stops accepting connections at once. Requests in progress may finish
  until the grace period ends, and are cut off then.
* It closes every websocket, including consoles, SCORCH terminals, VNC and
  port forwards, with close code `1001` (going away), so the web UI says the
  server is shutting down rather than that the network failed. New websockets
  are refused with `503 Service Unavailable`.
* Running background jobs get the first half of the grace period to finish,
  and are canceled then. Queued jobs are canceled at once, and starting a job
  or a console answers `503 Service Unavailable`.
* It ends minimega consoles and SCORCH terminals, so their processes do not
  outlive the server.
* It logs `shutdown complete` once everything has stopped, or a warning that
  names what was still running when the grace period ended, and exits with
  status 0 either way.

A second `SIGINT` or `SIGTERM` stops the server at once, with exit status 130
or 143. `docker stop` waits 10
seconds before it kills the container, which covers the default. If you raise
`ui.shutdown-grace-period` past that, raise Docker's timeout with it:
`stop_grace_period` in `docker-compose.yml`, or `docker stop --time`.

## The Settings Page

The web UI's `Settings` tab needs `update` on `settings`. Unlike the settings
above, these are kept in the data store and take effect as soon as they are
saved, with no `config.yaml` change or restart.

| Section | Setting | Default |
| :--- | :--- | :--- |
| `Password Settings` | `Require a lowercase letter`, `Require a number`, `Require a symbol` | on |
| `Password Settings` | `Require an uppercase letter` | off |
| `Password Settings` | `Minimum length of password`, 8 to 32 | `8` |
| `Timeout Settings` | `Log out users after period of inactivity` | off |
| `Timeout Settings` | `Time (minutes) to log out users after idle for` | `30` |
| `Timeout Settings` | `Display idle user logout with (minutes) left` | `3` |
| `File Logging Settings` | `Max log file size (MiB)` of the system log | `100` |
| `File Logging Settings` | `Max number of file rotations (0 for infinite)` | `0` |
| `File Logging Settings` | `Max rotated log file age (0 for infinite)`, in days | `90` |

The two idle timeout boxes are grayed out until the timeout is turned on. The
page opens on the settings it last loaded, and the refresh button in the header
reloads them without discarding edits you have not saved. `Reset Form` puts
the loaded settings back, `Save Changes` saves them and says
`Settings updated`, and both stay grayed out until you change something.

## phēnix Apps Environment Variables

Apps (`phenix-apps`) run as subprocesses and read their configuration from the environment.
phēnix sets these itself when it launches an app:

| Environment Variable | Value | Description |
| :--- | :--- | :--- |
| `PHENIX_DIR` | `base-dir.phenix` | Base phēnix data directory. |
| `PHENIX_FILES_DIR` | experiment files directory | Where the app reads and writes experiment files. |
| `PHENIX_LOG_LEVEL` | phēnix's own value, else `DEBUG` | App log verbosity. |
| `PHENIX_LOG_FILE` | `stderr` | App logs stream back to phēnix rather than to a file. |
| `PHENIX_DRYRUN` | `true` / `false` | Whether the run is a dry run. |
| `PHENIX_STORE_ENDPOINT` | `store.endpoint` | Data store endpoint (user apps only). |
| `PHENIX_SCORCH_STARTTIME` | run start time | Scorch components only. |

The rest are inherited from the phēnix process:

| Environment Variable | Default | Description |
| :--- | :--- | :--- |
| `PHENIX_TEMP_DIR` | `/tmp/phenix` | App temporary directory. |
| `MM_FILEPATH` | `/phenix/images` | Base minimega file path. |
| `MM_SOCKET_PATH` | `/tmp/minimega/minimega` | minimega command socket. |

### miniccc (`cc`) Timing

Apps drive VMs over minimega's `cc` command-and-control channel. These bound how long an app
waits, in seconds.

| Environment Variable | Default | Description |
| :--- | :--- | :--- |
| `PHENIX_CC_POLL_RATE` | `2.0` | Interval between `cc` polls. |
| `PHENIX_CC_CLIENT_GRACE` | `300.0` | Wait for a miniccc client to register before failing. |
| `PHENIX_CC_SEND_GRACE` | `300.0` | Wait for a file send or component start to be acknowledged. |
| `PHENIX_CC_CMD_GRACE` | `0.0` | Wait for a command response. `0` waits indefinitely, supervised by client liveness instead. |
| `PHENIX_CC_EXITCODE_GRACE` | `10.0` | Wait for an exit code once the response is counted. |
| `PHENIX_CC_LIVENESS_INTERVAL` | `10.0` | Interval between client-liveness checks during an unbounded wait. |
| `PHENIX_CC_LOG_INTERVAL` | `10.0` | Delay before the first "still waiting" log line. |
| `PHENIX_CC_LOG_MAX_INTERVAL` | `320.0` | Ceiling for that interval, which doubles each time. |
