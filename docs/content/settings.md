# Settings & Configuration

phēnix supports dynamic configuration management via the `phenix settings` command.
A running `phenix ui` applies changes to the `log.*` settings, `ui.logs.level`,
`experiment.disk-check`, `minimega.read-connections` and the users file at
once, without restarting. It reads every other setting only at startup.

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
phenix no longer supports the `etcd://` store endpoint.

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

`phenix settings set` gives the value the type of the flag of the setting. For
example, `log.system.max-size` takes an integer, `ui.jwt-lifetime` takes a
duration such as `24h`, and `ui.users` takes a comma-separated list. A string
setting, such as `ui.jwt-signing-key`, keeps the text as it is, so `0123` stays
`0123`. The command refuses a value that does not have the type of the setting.

`phenix settings set` and `phenix settings unset` change only the key. The
comments, the key order and the other keys in the file stay. A new key goes at
the end of its section. An empty section, such as `{}`, changes to block style
when you set a key in it. When you unset a key that is not in the file, the
file does not change.

## Checking the Configuration

`phenix doctor` checks the configuration without a running `phenix ui`. It
runs these checks:

| Check | Group | Warns when | Fails when |
| :--- | :--- | :--- | :--- |
| `config-file`, `users-file` | `config` | no `config.yaml` is found | the file does not parse or cannot be read, so phēnix ignores it; for a parse error the row gives the line, never the file's content |
| `config-keys` | `config` | the config files hold keys that are not phēnix settings, such as a misspelling | never |
| `config-paths` | `config` | phēnix ignores a config or users file on the search path, since it reads only the first of each name; the row lists the directories searched and each file found | never |
| `settings-file` | `config` | `phenix settings set` would write a file other than the config file phēnix reads, a file that is not YAML, or a file this user cannot write or create | never |
| `scenario-hosts` | `config` | an app in the scenario of a stored experiment names a host (`hosts[].hostname`) that is not a node of the topology of the experiment. The apps of phēnix find a host by its exact host name, and skip a host that they do not find. A `skip` row shows when doctor cannot read the store, or when the store file does not exist | never |
| `env` (one row per set variable, named after it) | `env` | a `PHENIX_*` variable is empty, a flag overrides it, phēnix reads it as a value other than the one written (such as `010` as `8`), it holds a log level phēnix ignores, or it names no setting, such as a misspelling; `PATH` has relative entries; `SUDO_USER` names no user when root runs phēnix through `sudo` | phēnix would refuse or misread a `PHENIX_*` value; `EDITOR` is not a program on `PATH` |
| `env-overrides` | `env` | a `PHENIX_*` variable overrides a different value in the config or users file; the row names the variable, the setting and the file, never a value | never |
| `proxy-env` | `env` | a proxy variable is set, and `NO_PROXY` or `no_proxy` does not cover a host of `phenix ui`. The proxy variables are `HTTP_PROXY`, `HTTPS_PROXY`, `http_proxy` and `https_proxy`. The hosts are `localhost`, `127.0.0.1`, `::1`, the host name and the host of `ui.listen-endpoint`. Programs that read these variables send their requests for such a host to the proxy. The row reads the forms `*`, `.domain`, `*.domain`, a CIDR range and `host:port`, as Go does. It names the variables, never their values, because a proxy URL can hold a password | never |
| `bridge-mode`, `deploy-mode`, `experiment.disk-check`, `color`, `log.level`, `log.system.max-size`, `log.system.max-backups`, `log.system.max-age`, `minimega.read-connections` | `settings` | `log.level` is not a level phēnix knows, so it logs at `info`; a `log.system.*` value is not a whole number, so phēnix reads `0` (100 MB for `max-size`, no limit for the others); `max-backups` or `max-age` is negative; `minimega.read-connections` is not a whole number from 3 to 64, so phēnix uses the nearest value in that range | `bridge-mode`, `deploy-mode`, `experiment.disk-check` or `color` is a mode every phēnix command refuses; `log.system.max-size` is negative, so every write to the system log fails |
| `store.endpoint` | `files` | never | the file cannot be written, or its directory does not exist or cannot be written |
| `store-file` (only when the store file exists) | `files` | one of its two meta pages is corrupt, so BoltDB reads the other, which may not hold the last write | the file is not a BoltDB file, has another format version or byte order, is shorter than its two meta pages, or both meta pages are corrupt |
| `store-owner` (only when the store file exists) | `files` | another user owns the file and this user is not root; as root under `sudo phenix`, the file does not belong to the user whose store `sudo phenix` uses | never |
| `store-root` (only for the default store of a user other than root) | `files` | `/etc/phenix/store.bdb` exists, or this user cannot see whether it exists, so a `phenix ui` that runs as root uses a different store | never |
| `store-fs` | `files` | the directory of the store file is on a network filesystem, such as NFS, SMB, CIFS, Ceph, 9p, Lustre, GPFS, GFS2 or AFS. Or it is on a FUSE filesystem, such as sshfs. On these filesystems, the file locks and `fsync` that BoltDB needs can fail. The row also warns when a store file of more than 100 MiB has more than 50% free pages. `bbolt compact` makes such a file smaller. doctor reads the free page list from the file without the lock, so it does not wait for `phenix ui`. doctor reads the filesystem type only on Linux, and gives a `skip` row when it can check neither part | never |
| `log.system.path`, `log.console` | `files` | never | the file (`phenix.log` in it when `log.system.path` is a directory) cannot be written, or created in the nearest directory above it that exists; `log.console` gets a row only when it names a file |
| `secret-files` | `files` | a file that holds a secret is readable by its group or by others. These files are the TLS key (`ui.tls-key`), each config or users file that sets `ui.jwt-signing-key` or `ui.users`, and the store file. The store file holds password hashes and tokens. The row also warns when another user owns the TLS key or such a config file. Only root and the user that runs phēnix can own them. doctor reads only the file information, never the contents. On a system without Unix file modes, the row is `skip` | never |
| `inject-sources` | `files` | the source (`src`) of a node injection does not exist on this host, so minimega `disk inject` cannot copy it at the start. For a stored experiment, doctor checks the injections of each VM that gets injects: `snapshot: true` and an inject partition other than 0. It finds a relative source in the `baseDir` of the experiment. It does not check an absolute source in the `baseDir`, because apps write those files at the start. For a stored topology, doctor checks only the absolute sources. A `skip` row shows when doctor cannot read the store | never |
| `base-dir.phenix`, `base-dir.minimega`, `base-dir.injects`, `base-dir.topologies`, `mount-dir` | `dirs` | the directory does not exist, is not readable and searchable by this user, is relative, or is one phēnix writes to and is not writable by this user | the path is not a directory |
| `base-dir.builder-templates` | `dirs` | the path is not a directory, the directory is not readable and searchable by this user, or the path is relative. The row also warns when the setting names a path that does not exist, and when `phenix ui` skips a template file in the directory. The row names each skipped file, at most 5, but never why: `phenix ui` logs the reason at start. A missing directory passes when the setting is not set, because `phenix ui` only reads the directory and never makes it | never |
| `ui.shutdown-grace-period`, `ui.netflow.max-size`, `ui.captures.max-size` | `ui` | never | `phenix ui` would refuse the value |
| `ui.jwt-signing-key` | `auth` | the key is empty (no sign-in) or a development key | a development key does not name exactly a user and a role, so `phenix ui` refuses every request |
| `ui.jwt-lifetime` | `auth` | never | phēnix reads the value as under a minute: `24` is 24 nanoseconds, and a value that is not a duration, such as `1d`, is 0 |
| `ui.users` | `auth` | an entry's role is not built in, so `phenix ui` skips the user unless the store holds the role; the list is empty while sign-in is on, so `phenix ui` creates the default user `admin@foo.com` | an entry is not `name:password:role[:resource...]`, which `phenix ui` skips without an error, or its role holds a comma; a row names an entry by position and user name, never by its password |
| `proxy-jwt` (only with the `proxy-jwt` key) | `auth` | `ui.listen-endpoint` listens on every address, and `phenix ui` does not check the signatures of proxy JWTs | never |
| `ui.proxy-auth-header` (only when set) | `auth` | the JWT signing key is empty or a development key, so `phenix ui` ignores the header | the value is not an HTTP header name |
| `rbac-users` (only with sign-in on) | `auth` | the role (`rbac.roleName`) of a stored user is not a stored role or a built-in role. Or a role names an experiment, a VM (`<experiment>/<vm>`) or a user that the store does not have. The row checks each stored role and the copy of the role in each user. The row checks only the names without a glob (`*`, `?`, `[`) or `!`. It checks them only in a policy for experiments and VMs, or for users. It never shows a password or a token. A `skip` row shows when doctor cannot read the store | never |
| `ui.tls` | `tls` | never | only one of `ui.tls-key` and `ui.tls-cert` is set, so `phenix ui` serves plain HTTP; a file cannot be read; the key and certificate do not load as a pair |
| `ui.tls-cert` (only when both TLS settings are set) | `tls` | the certificate expires within 30 days, or it has no DNS or IP names. The row also warns when the names do not cover the host that users reach. That host is the host of `ui.listen-endpoint`, or the host name when `phenix ui` listens on every address. A host name without a domain, such as `ws42`, also matches a name such as `ws42.example.com`. The row gives the expiry date and the names. A `skip` row shows when the key pair does not load | the certificate expired or is not valid yet |
| `ui.listen-endpoint`, `ui.file-server-endpoint` | `tls` | the listen endpoint has no port, so `phenix ui` listens on a random one | `phenix ui` cannot listen on the value or refuses it |
| `unix-socket` | `socket` | the path is relative, or holds a file that is not a socket, which `phenix ui` deletes; this user cannot write the directory, but a socket is there, such as one a `phenix ui` of another user serves | the directory does not exist, this user cannot write it and no socket is there, or the path is too long for a unix socket |
| `unix-socket-gid` | `socket` | phēnix runs as root and no group has the id | phēnix does not run as root, and this user is not in a group of that id, so `phenix ui` exits when it sets the socket's group |
| `server` (with the rows server bridge-mode and server deploy-mode when phenix ui answers) | `server` | no `phenix ui` answers on `unix-socket`: no socket file, a stale one, a file that is not a socket, or no answer within `--timeout`; the version of `phenix ui` is not the version of this phēnix; its `bridge-mode` or `deploy-mode` is not the one set here | this user may not connect to the socket |
| `stuck-status` | `server` | the store marks app runs, SCORCH runs or delayed starts of a running experiment as in progress, but no phenix process can own them. That is, no `phenix ui` answers on `unix-socket`, and no other phenix process on this host names the experiment. This occurs when `phenix ui` stops during a start or a run. The row also warns when phēnix could not resume the start of a running experiment, and when the store marks a start as in progress that no phenix process runs. A `skip` row shows when doctor cannot read the store | never |
| `ui-limits` (Linux only) | `server` | the `phenix ui` process on this host has more than 80% of its soft limit of open files open. doctor finds the process from the peer of `unix-socket`, or from its command line in `/proc`. A `skip` row shows when no `phenix ui` runs on this host, or when this user cannot read `/proc/<pid>` | never |
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
| `disk` (one row per filesystem, such as disk phenix-base) | `tools` | the filesystem of `base-dir.phenix`, the store or minimega's `-filepath` is 90% full | the filesystem is 98% full |
| `kernel-modules` (Linux only) | `tools` | `/lib/modules/<kernel release>` does not exist, so `modprobe` cannot load modules such as `nbd` and `openvswitch`; `/var/run/reboot-required` exists. A container without `/lib/modules` gives a `skip` row | never |
| `nbd` (Linux only) | `tools` | the `nbd` module is not loaded, is part of the kernel (so `lsmod` does not show it), or has `max_part` missing or 0; no `/dev/nbdN` device is free, or a container has no `/dev/nbd*` device; a device has a lock file in `/var/lock` and no connection, belongs to a process that is not running, or holds an image for a `qemu-nbd` that a failed inject can leave, with its mounted partition. minimega `disk inject` uses these devices for injects at a start and for `phenix image inject-miniexe`. A row for a missing module points to `kernel-modules` | `qemu-nbd` is not on `PATH` |
| `openvswitch` (Linux only) | `tools` | this user cannot connect to `ovsdb-server` (permission denied), but minimega runs `ovs-vsctl` as root; `ovs-vswitchd` does not run; the `openvswitch` module is not loaded, and the row points to `kernel-modules`. The row gives the Open vSwitch version, and tells if the bridge `phenix` exists or if minimega makes it at the first VM launch. In a container without `ovs-vsctl`, the row is `skip` | `ovs-vsctl` is not on `PATH`; `ovs-vsctl show` gets no answer from `ovsdb-server` within `--timeout` |
| `image-build` (the rows image-build tools, image-build root, image-build loop and image-build keyrings; Linux only) | `tools` | `debootstrap`, `kpartx`, `losetup` or `qemu-img` is not on `PATH`; the process does not have `CAP_SYS_ADMIN` (not root, or a container that is not privileged); no `/dev/loop*` device is free and there is no `/dev/loop-control`; debootstrap has no script for the release of a stored image config, or the keyring that the script names does not exist. The keyrings row reads the image configs from the store read-only, and gives `skip` when the store is locked for longer than `--timeout` | never |
| `image-configs` | `tools` | an overlay directory (`overlays`) of a stored image config does not exist or is not a directory, so `phenix image build` fails. The row also names each script file of a config that is not on disk, without a warning. The config holds a copy of each script, which the build uses and `phenix image update` keeps | never |
| `disk-chains` | `images` | a disk image in the images directory has a broken backing chain: a backing file is missing, the chain loops, or the chain is more than 64 images deep; the row names at most 5 such images and counts the rest; the directory cannot be listed; the check does not finish within `--timeout` | never; the images directory is minimega's `-filepath`, or `<base-dir.phenix>/images` when minimega gives none, and a `skip` row shows when it does not exist |
| `disk-formats` (a second row when an image needs a later qemu) | `images` | a disk image in the images directory is in an old or unsupported format: qcow2 version 2, qcow version 1, vmdk or qed. It also warns about an image that qemu refuses, such as an encrypted or corrupt image, and when the local `qemu-img` is older than the qemu version that a feature of an image needs. The row names at most 5 such images and counts the rest. It also warns when it cannot list the directory, or when the check does not finish within `--timeout`. | never. It reads the headers that the `disk-chains` check reads, in the same pass over the images directory, and a `skip` row shows when that directory does not exist |
| `writable-images` | `images` | a VM of a stored experiment has `snapshot: false`, so minimega writes to its drive images. The row warns when another image in the images directory has such an image as a backing file, because the writes break that image. It also warns when more than one VM uses such an image. It reads the experiments from the store, and the backing files from the pass of `disk-chains` | never. A `skip` row shows when the store cannot be read, or when the store file does not exist |
| `leftovers` (the rows leftovers snapshots, leftovers files, leftovers mounts, leftovers taps and leftovers mirrors) | `images` | the snapshots row: a snapshot file of this head node in the images directory (`<head node>_<experiment>_<VM>_snapshot`) belongs to an experiment that the store does not have. The files row: a base directory in `<base-dir.phenix>/experiments`, or a directory with `files`, `miniccc_responses` or a `minirouter-<VM>` file in the images directory, belongs to such an experiment. The row gives each size and the total. The mounts row: a VM file system mount under `mount-dir` belongs to such an experiment, or to a VM that minimega does not run. The taps row: a minimega tap (`mega_tap*`) on this host or on an Open vSwitch bridge has no owner, or a port has no network device. An owner is a VM or a host tap in a minimega namespace, or a capture of a whole bridge. The mirrors row: a minimega mirror (`mirror-<tap>`) belongs to no capture. The rows keep what belongs to a namespace in which minimega runs a VM. In a container, the taps row checks the taps of this host only when it shows a minimega tap, an Open vSwitch bridge or the `ovs-system` device. A row that cannot get its data from the store, minimega or `ovs-vsctl` is a `skip` row with the reason. A store file that does not exist gives a `skip` row, because `phenix doctor` can read a store other than the store of `phenix ui` | never |
| `disk-space` (one row for each filesystem, such as disk-space files, and the row disk-space inject dirs) | `images` | the filesystem of the images directory, of minimega's `-base` or of minimega's `-logfile` has less free space than 10% of its size or 5 GiB, whichever is larger. The row names each filesystem once. It gives the size of the minimega log, and the size of the files that the `leftovers` rows found. The inject dirs row warns about each `dstImg*` directory in minimega's `-base`, which a failed `disk inject` can leave, and tells which ones are mounted | never |
| `image-locks` (Linux only) | `images` | a `qemu-system`, `qemu-nbd` or `qemu-img` process holds open a drive image of a stopped experiment, or one of its backing files, and is not a VM of a running experiment. qemu then cannot get the lock it needs to start the VM (`Failed to get "write" lock`). Any open of an image that a VM writes to (`snapshot: false`) blocks the start. An open of another image blocks it only when the process can write to the file. The row tells how many processes this user cannot read | never. A `skip` row shows in a container that shows no minimega process, because the container can have its own PID namespace |
| `inject-partitions` | `images` | the inject partition of a VM of a stored experiment is not one that minimega can write files to at a start. The row checks each VM with `snapshot: true` and an `inject_partition` other than 0 on its first drive, which gets injects, deletions or the scripts of the `startup` app. The partition numbers are the numbers that Linux and minimega use: 1 to 4 for MBR primary partitions, 5 and up for logical partitions, and the entry number for GPT. The row warns when the disk has no partition table, when it has no partition 1 (minimega waits for partition 1 before it opens any partition, so the start fails with `no partitions found on image`), when it has no partition of that number (`desired partition N not found`), or when the partition is an extended partition. It also warns when the partition holds swap, an LVM physical volume, a LUKS volume, a read-only file system, or no file system that doctor knows. It also warns when the partition is smaller than 1 GiB and the disk has a larger Linux or NTFS partition, such as a Windows `System Reserved` partition. The row gives the VMs, the image, the partitions it found (number, type, file system and size), and the largest Linux or NTFS partition as the `inject_partition` to try. It also warns when an NTFS inject partition is dirty or Windows did not shut down fully (hibernation or Fast Startup), or when an ext file system has errors. doctor reads each image once, at most 16 MiB of it and 512 KiB more for the file system states, a qcow2 or qcow image through its backing chain with `qemu-img dd -U`. | never. The row does not check an image that does not exist, a qcow2 image when `qemu-img` is not installed, or a partition past the part of the disk that doctor reads. A `skip` row shows when doctor checks no inject partition, or cannot read the store |
| `apps` | `apps` | never | never; the row lists the user apps (`phenix-app-*`) and SCORCH components (`phenix-scorch-component-*`) found on `PATH` |
| `scenario-apps` | `apps` | an app of a stored scenario or experiment is not built in, and `PATH` has no `phenix-app-<app>`. Or a SCORCH component type (its `type`, else its `name`) is not built in, and `PATH` has no `phenix-scorch-component-<type>`. The built-in apps are `ntp`, `serial`, `startup`, `vrouter`, `tap`, `soh` and `scorch`. The built-in SCORCH components are `break`, `pause`, `soh` and `tap`. A `skip` row shows when doctor cannot read the store | never |
| `app-runtimes` | `apps` | a `phenix-app-*` or `phenix-scorch-component-*` program on `PATH` is not executable by this user, so phēnix does not find it. Its `#!` line names an interpreter that does not exist or that this user cannot run, or a program that `env` cannot find on `PATH`. Its `#!` line ends in a carriage return (Windows line ends) | never |
| `tunneler-download`, `webshark` | `features` | `downloads/tunneler` is not a directory | never; the row says whether `phenix ui` turns the feature on |
| `ui.features` | `features` | a name is not a feature, such as a misspelling; `tunneler-download` or `file-server` is set without what it needs | `webshark` is set without a WebShark build, which `phenix ui` refuses |
| `minimega-captures` | `features` | never | never; the row says that `phenix ui` uses the capture commands with all minimega versions, and shows the version that the `minimega` check read |
| `ui.logs.minimega-path` | `features` | the file does not exist or is not readable by this user; the setting is empty while minimega logs to a file (its `-logfile`, or `MM_LOGFILE` in the environment) | the path is not a regular file |

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

## Global Flags

The following global flags are supported by all `phenix` subcommands. The
[CLI reference](cli/index.md) lists them with every command's own flags.

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--base-dir.builder-templates` | `string` | `<base-dir.phenix>/builder/templates` | Directory of Builder node template files that `phenix ui` reads at start. See [Template files on the server](builder/details/administration.md#template-files-on-the-server). |
| `--base-dir.injects` | `string` | `<base-dir.phenix>/injects` | Directory that `phenix workflow apply` stages topology injects into. The `phenix ui` server reports its value to the CLI, and the flag on the `phenix workflow apply` command line overrides it. See [Workflow](workflow.md). |
| `--base-dir.minimega` | `string` | `/tmp/minimega` | Base minimega directory. |
| `--base-dir.phenix` | `string` | `/phenix` | Base phēnix directory. |
| `--base-dir.topologies` | `string` | `<base-dir.phenix>/topologies` | Directory that `phenix workflow apply` looks up topology directory names in. See [Workflow](workflow.md). |
| `--bridge-mode` | `string` | `manual` | Bridge naming mode for experiments (`auto` uses experiment name for bridge; `manual` uses user-specified bridge name, or `phenix` if omitted). A `phenix ui` running in `auto` mode makes the commands that reach it over `--unix-socket` use `auto`. See [Bridge Mode](bridge-mode.md). |
| `--color` | `string` | `auto` | When to color the output (`auto`, `on`, or `off`). `auto` colors only a terminal, and not when `NO_COLOR` is set or `TERM` is `dumb`. |
| `--deploy-mode` | `string` | — | Deploy mode for minimega VMs (`all`, `no-headnode`, or `only-headnode`). |
| `--experiment.disk-check` | `string` | `error` | What a start does when a VM disk image or CD-ROM image is missing, or its backing chain is broken (`error`, `warn`, or `off`). |
| `-h, --help` | — | — | Display help information for the command. |
| `--hostname-suffixes` | `string` | `-minimega,-phenix` | Hostname suffixes to strip. |
| `--log.console` | `string` | `stderr` | Destination output for console logs in text format (`stderr`, `stdout`, or a file path). |
| `--log.level` | `string` | `info` | Level to log messages at (`debug`, `info`, `warn`, `error`). |
| `--log.system.max-age` | `int` | `90` | Maximum number of days to retain old log files. |
| `--log.system.max-backups` | `int` | `3` | Maximum number of old log files to retain. |
| `--log.system.max-size` | `int` | `100` | Maximum size in megabytes of the system log file before rotation. |
| `--log.system.path` | `string` | `/var/log/phenix/phenix.log` | Path to persistent system log in JSON Lines format, with one JSON object per line. |
| `--minimega.read-connections` | `int` | `16` | Maximum number of read-only commands that phēnix sends to minimega at the same time, from 3 to 64. phēnix keeps 2 of them for reads of the head node. |
| `--mount-dir` | `string` | `<base-dir.phenix>/mounts` | Base directory for VM filesystem mounts. |
| `--store.endpoint` | `string` | `bolt:///etc/phenix/store.bdb` | BoltDB store endpoint (`bolt://<path>`). |
| `--unix-socket` | `string` | `/tmp/phenix.sock` | phēnix unix socket to listen on (`ui` subcommand) or connect to. |
| `--use-gre-mesh` | `bool` | `false` | Use GRE tunnels between mesh nodes for VLAN trunking. See [GRE Mesh](gre-mesh.md). |

## Settings Reference

| Setting Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `bridge-mode` | `PHENIX_BRIDGE_MODE` | `manual` | Bridge naming mode for experiments. `auto` uses the experiment name as the bridge name; `manual` uses the user-specified bridge name, or `phenix` if not specified. See [Bridge Mode](bridge-mode.md). |
| `experiment.disk-check` | `PHENIX_EXPERIMENT_DISK_CHECK` | `error` | What a start does when a VM disk image or CD-ROM image is missing, or its backing chain is broken. `error` refuses the start, `warn` starts the experiment and shows each problem as a warning, and `off` does not check the images. It applies to every start: the CLI, the web UI, the REST API, `phenix workflow apply`, and the bulk start and restart of VMs. phēnix refuses another value. `phenix ui` applies a change at once. |
| `use-gre-mesh` | `PHENIX_USE_GRE_MESH` | `false` | Enable Layer 2 GRE tunnels for the default bridge of experiments created or updated with this setting. See [GRE Mesh](gre-mesh.md) for usage, server inheritance, and MTU requirements. `phenix ui` reads this setting at startup. **(Restart Required)** |
| `log.level` | `PHENIX_LOG_LEVEL` | `info` | Global log verbosity (`debug`, `info`, `warn`, `error`). |
| `color` | `PHENIX_COLOR` | `auto` | When phēnix colors its output: statuses in tables, `phenix doctor` rows, warnings and errors. `auto` colors only a terminal, and not when `NO_COLOR` is set or `TERM` is `dumb`. `on` and `off` ignore both variables. JSON and YAML output never have color. phēnix refuses another value. |
| `log.console` | `PHENIX_LOG_CONSOLE` | `stderr` | Destination for console logs (`stderr`, `stdout`, or a file path). Uses **Text/Human-Readable** format. Note: Setting this to a file path will prevent console logs from appearing in `docker logs`. |
| `log.system.path` | `PHENIX_LOG_SYSTEM_PATH` | `/var/log/phenix/phenix.log` | Path to the persistent system log file (used by UI). Uses **JSON** format. This is independent of `log.console` and is always active. |
| `log.system.max-size` | `PHENIX_LOG_SYSTEM_MAX_SIZE` | `100` | Max size in MB before rotation. |
| `log.system.max-backups` | `PHENIX_LOG_SYSTEM_MAX_BACKUPS` | `3` | Number of old log files to retain. |
| `log.system.max-age` | `PHENIX_LOG_SYSTEM_MAX_AGE` | `90` | Max age in days to retain old logs. |
| `ui.logs.level` | `PHENIX_UI_LOGS_LEVEL` | `""` | Log level for the web UI stream (defaults to `log.level`). |
| `minimega.read-connections` | `PHENIX_MINIMEGA_READ_CONNECTIONS` | `16` | Maximum number of read-only commands, such as `vm info`, that phēnix sends to minimega at the same time. Each command uses a connection of its own, which phēnix opens when it needs one and closes after 5 minutes without use. The value must be from 3 to 64. phēnix uses the nearest value in that range for a value outside it, and logs a warning. phēnix keeps 2 connections for reads of the head node only, and lets screenshots use at most 4 fewer than the maximum. It applies to `phenix ui` and to every CLI command. `phenix ui` applies a change in the config file at once, unless a flag or an environment variable sets the value. The Settings page of the web UI also changes it. |
| `ui.logs.minimega-path` | `PHENIX_UI_LOGS_MINIMEGA_PATH` | `""` | Path to the minimega log file to display in the UI and offer for download on the Logs page. **(Restart Required)** |
| `ui.features` | `PHENIX_UI_FEATURES` | `""` | Optional UI features to enable. Separate them with commas in the `--features` flag and with spaces in `PHENIX_UI_FEATURES`. Use a YAML list in the configuration file. Currently supports `vm-mount`, which enables transferring files to and from a running VM (see [Mount a VM](vms.md#mount-a-vm)). `minimega-captures` has no effect: `phenix ui` uses the capture commands with all minimega versions. phēnix accepts the name, so that existing configurations do not fail. `phenix ui` turns on `webshark`, `tunneler-download` and `file-server` itself, and refuses to start with `webshark` set but no WebShark build. **(Restart Required)** |
| `ui.file-server-endpoint` | `PHENIX_UI_FILE_SERVER_ENDPOINT` | `0` (disabled) | Address (`port` or `host:port`) for the separate experiment file-upload server. A port-only value binds to `127.0.0.1`. Also works with the `vm-mount` feature - see [Uploading Experiment Files from the phēnix Server](vms.md#uploading-experiment-files-from-the-phenix-server). If it is set and the address cannot be listened on, `phenix ui` exits with an error. **(Restart Required)** |
| `ui.shutdown-grace-period` | `PHENIX_UI_SHUTDOWN_GRACE_PERIOD` | `5` | How many seconds stopping `phenix ui` may take to finish requests and jobs and close connections, as a whole number such as `10`. `0` closes everything without waiting; a negative value, or one that is not a whole number of seconds (such as `10s` or `1.5`), is refused at startup. Also `phenix ui --shutdown-grace-period`. **(Restart Required)** |
| `ui.netflow.max-size` | `PHENIX_UI_NETFLOW_MAX_SIZE` | `100` | Maximum size in megabytes of each recorded netflow capture, as a whole number. A capture that reaches it stops recording and is marked truncated, while the live view goes on. `0` does not record captures; a negative value, or one that is not a whole number, is refused at startup. Also `phenix ui --netflow.max-size`. **(Restart Required)** |
| `ui.captures.max-size` | `PHENIX_UI_CAPTURES_MAX_SIZE` | `10240` | Maximum size in megabytes of each packet capture file that minimega writes, as a whole number. `0` sets no limit; a negative value, or one that is not a whole number, is refused at startup. Also `phenix ui --captures.max-size`. **(Restart Required)** |
| `mount-dir` | `PHENIX_MOUNT_DIR` | `<base-dir.phenix>/mounts` | Base directory used for VM filesystem mounts created via `phenix vm mount`. See [Mount a VM](vms.md#mount-a-vm). |
| `base-dir.injects` | `PHENIX_BASE_DIR_INJECTS` | `<base-dir.phenix>/injects` | Directory that `phenix workflow apply` stages a topology directory's `phenix-injects/` into, as `<base-dir.injects>/<name>`. The phēnix server's value is used unless `--base-dir.injects` is given; see [Workflow](workflow.md#settings). Use an absolute path. **(Restart Required)** |
| `base-dir.topologies` | `PHENIX_BASE_DIR_TOPOLOGIES` | `<base-dir.phenix>/topologies` | Directory where `phenix workflow apply NAME` looks for `NAME` when `NAME` isn't an existing path. Use an absolute path. See [Workflow](workflow.md). |
| `base-dir.builder-templates` | `PHENIX_BASE_DIR_BUILDER_TEMPLATES` | `<base-dir.phenix>/builder/templates` | Directory of Builder node template files (`*.yaml`, `*.yml`, `*.json`) that `phenix ui` reads at start as read-only server collections. Use an absolute path. See [Template files on the server](builder/details/administration.md#template-files-on-the-server). **(Restart Required)** |

## Web UI Session Timeout

The web UI's **Settings** page (admin-only) can also automatically log out idle users. Unlike
the settings above, this is stored in the data store and takes effect immediately when saved —
no `config.yaml` or restart involved.

| Setting | Default | Description |
| :--- | :--- | :--- |
| Log out idle users | Disabled | Enables the idle timeout. |
| Idle timeout (minutes) | `30` | Minutes of inactivity before an idle user is logged out. The minimum is `0.5`. |
| Warning before logout (minutes) | `3` | How long before logout to warn the user. This value must be less than the idle timeout. |

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
