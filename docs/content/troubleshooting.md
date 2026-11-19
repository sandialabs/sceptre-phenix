# Troubleshooting

This page consolidates common issues and their solutions when working with phēnix.

Most of the problems below have a [`phenix doctor`](doctor.md) check that
finds them, so run it first. To report a bug, paste the output of
`phenix doctor --report` (`docker exec phenix phenix doctor --report` in
Docker) into the issue (see
[Reporting a Bug](doctor.md#reporting-a-bug)).

## Logging Issues

### Logs Not Appearing in UI

If your application logs are not showing up in the phēnix web UI, check the following:

* **Check the stream**: Ensure your app writes logs to `stderr`. `stdout` is reserved for returning modified experiment configuration JSON to phēnix.
* **Check the format**: The phēnix core expects valid, single-line JSON objects. If your app prints raw text (like `Starting app...`), it may be logged as a warning or error by the core but will not be indexed correctly for the UI.
* **Check the environment**: Verify `PHENIX_LOG_FILE=stderr` is set in your app's environment (phēnix does this automatically for apps it launches).
* **Check for skipped lines**: When the server logs faster than it can send to the browser, the Logs page's live view skips lines and shows a `WARN` entry such as `120 log lines skipped while the server was busy`. The lines are still in the log file: reload the Logs page, or call `GET /logs`, to see them. The server also logs a `WebSocket broker dropped slow clients or live log lines` warning at most every 30 seconds.

### No minimega Logs on the Logs Page

The Logs page shows minimega's log only when `ui.logs.minimega-path` names the
file minimega logs to, such as `/var/log/phenix/minimega.log` in the Docker
Compose setup. If the file isn't there, `phenix ui` waits for it without a
warning.

### JSON Parsing Errors

If you see "malformed log entry" warnings in the main phēnix system log, it's likely because your application is writing non-JSON text to `stderr`.

* **Debug Prints**: Ensure you are not using `print()` (Python) or `fmt.Println()` (Go) for debugging. These pollute the `stderr` stream. Use the structured logger.
* **Tracebacks**: Ensure exception tracebacks are not being printed as raw text.
    * **Python**: Use `logger.exception("message")` inside an `except` block. This correctly captures the traceback as a JSON field. Standard `print(e)` or `traceback.print_exc()` will break the parser.
    * **Go**: Use structured logging fields for errors (e.g., `slog.Error("message", "err", err)`).

### Inconsistent Timestamps

Ensure timestamps in your JSON logs are in a consistent format that phēnix can parse, such as RFC3339. The core now enforces a `2006-01-02 15:04:05.000` format for its own file logs for easier reading, but is flexible on ingest.

## Packet Capture Issues

### Experiment Captures Are Not Available

Experiment captures need minimega 3.4.0 or later on every node. phēnix checks
the minimega version of the head node. For the disabled controls, a
refused start, and a skew warning, see
[Experiment Packet Captures](packet-captures.md#troubleshooting).

## Configuration Issues

### Settings in `config.yaml` Have No Effect

If phēnix can't read its config file (or `users.yaml`), it falls back to
environment variables and defaults, and prints a warning on stderr at startup
(`docker logs phenix` in Docker):

```text
phenix: warning: ignoring config file /etc/phenix/config.yaml: it does not parse at line 3
```

Fix the file at the line it names. `phenix doctor` also finds misspelled keys.

### Every Command Fails on a Mode Setting

Every command except `phenix doctor` and `phenix healthcheck` reads
`bridge-mode`, `deploy-mode`, `experiment.disk-check` and `color` first, and
stops on a value it doesn't know:

```text
setting user-specified bridge mode: setting bridge mode: unknown bridge mode provided: sideways
```

Fix the value in the flag, the `PHENIX_*` variable or the config file.

### Output Shows Escape Codes Such as `^[[32m`

phēnix colors its output when `--color` is `on`, or `auto` and the output is a
terminal, so a program that reads the output shows the codes as text. Use
`--color off`, `PHENIX_COLOR=off`, `NO_COLOR=1`, or `color: off` in the config
file (see
[Colors](settings.md#colors)).

### `phenix ui` Fails to Start, or Ignores TLS or Users

`phenix ui` exits at startup when it can't listen on `ui.listen-endpoint` or
`ui.file-server-endpoint`, or can't create its unix socket or give it the
`unix-socket-gid` group. It starts, but not as configured, when only one of
`ui.tls-key` and `ui.tls-cert` is set, a `ui.users` entry isn't
`name:password:role[:resource...]`, or `ui.jwt-lifetime` has no unit. To find
which, run this as the user of `phenix ui`, with its settings, and read the
`auth`, `tls` and `socket` rows:

```shell
phenix doctor --skip minimega
```

### Browsers Refuse the Certificate of `phenix ui`

Browsers refuse a certificate that has expired, isn't valid yet, or whose DNS
and IP names don't include the host users connect to (they ignore the common
name). `phenix doctor -p -x` shows the expiry date and names in its
`ui.tls-cert` row. Make a new certificate with that host in its names, set
`ui.tls-cert` and `ui.tls-key` to the new files, and restart `phenix ui`.

### Requests for `phenix ui` Go to a Proxy

Programs that read `HTTP_PROXY` or `HTTPS_PROXY` send their requests to the
proxy. The `proxy-env` row of `phenix doctor -p -x` names each host of
`phenix ui` that `NO_PROXY` or `no_proxy` doesn't cover. Add those hosts to
both, then restart `phenix ui` and the apps that run in the same environment.

### A Command Ignores `bridge-mode` or `deploy-mode`

Every command except `phenix doctor` and `phenix healthcheck` first asks
`phenix ui` on `unix-socket` for its modes. If `phenix ui` uses
`bridge-mode: auto`, so does the command, and if you didn't set `deploy-mode`
for the command, it uses the one from `phenix ui`.

### Do Not Delete `config.yaml`

If you delete the `config.yaml` file while phēnix is running, the internal file watcher will break, and dynamic configuration changes will no longer apply.

To reset your configuration, use the command `phenix settings unset --all` instead of deleting the file.

### `phenix settings set` Has No Effect

A flag or a `PHENIX_*` environment variable overrides the config file and hides
what `phenix settings set` writes (the Docker deployment sets `PHENIX_COLOR`,
for example). Drop the flag or unset the variable. `phenix doctor` shows where
each setting comes from.

### A Command Sees Different Experiments Than `phenix ui`

By default, root uses the store `/etc/phenix/store.bdb` and other users use
`$HOME/.phenix.bdb`, so if `phenix ui` runs as root and you don't, you're
looking at a different store. `sudo phenix` doesn't help, because it still
uses the store of the user that ran `sudo`. Run the command from a root shell
(`sudo -i`), or set `store.endpoint` to the store of `phenix ui`.

### Upgrading the BoltDB Store

You don't need to delete or recreate `store.bdb` after an upgrade, and earlier
releases can still open the store.

### The Store Is on a Network Filesystem or Has Many Free Pages

On a network filesystem (NFS, SMB) or a FUSE filesystem (sshfs), BoltDB's file
lock or write sync can fail, so two processes can write to one store or a write
can be lost. Set `store.endpoint` to a path on a local disk.

BoltDB also doesn't shrink the store file when phēnix deletes data. To compact
a store with many free pages:

1. Stop `phenix ui` and copy the store file to a backup.
2. Compact it with the bbolt tool:

    ```bash
    go install go.etcd.io/bbolt/cmd/bbolt@latest
    bbolt compact -o <new-file> <store-file>
    ```

3. Replace the store file with the new file, with the same owner and mode
   `0600`, and start `phenix ui`.

### Files That Hold Secrets Are Readable by Other Users

`phenix doctor` warns when the group or other users can read the TLS key, the
store file, or a config or users file that sets `ui.jwt-signing-key` or
`ui.users`, or when the key or such a config file belongs to someone other
than root or the user that runs phēnix. Run `chmod 600 <file>` on each file
it names, and `chown <user> <file>`, where `<user>` runs `phenix ui`, on each
file another user owns.

### An App or SCORCH Component Is Not Found

phēnix runs an app that isn't built in as `phenix-app-<app>` on `PATH`, and a
SCORCH component as `phenix-scorch-component-<type>`. If the program isn't on
`PATH`, or this user can't run it, the start or SCORCH run fails:

```text
external user app phenix-app-foo does not exist in your path: user app not found
```

Install it (in Docker, in the image) or add its directory to `PATH` for the
user that runs `phenix ui`, and make it executable with `chmod +x <path>`. If
its `#!` line names a missing interpreter, install the interpreter, and if the
line ends in a carriage return, run `dos2unix <path>`.

### Reserved Experiment Names

You can't name an experiment `minimega`, `__phenix__`, `all`, `.` or `..`, in
any mix of upper and lower case:

```text
cannot use 'minimega' for experiment name: reserved experiment name: it is a minimega namespace
```

## minimega Issues

### `timeout running command`

phēnix sends most minimega commands over one shared connection, one at a time,
so a command that waits for it or holds it too long fails with
`timeout running command`. A web request that changes VMs fails with an error
that includes `context deadline exceeded`. Either way, minimega is usually
stuck or overloaded: check `minimega -e version` and the minimega log on the
head node.

### A Start Fails on a Broken Backing Chain

phēnix won't start an experiment when a VM's disk or CD-ROM image, or a file in
a disk's backing chain, is missing:

```text
Error: unable to start the foo experiment: qemu cannot open 1 disk on 1 VM: 1 error occurred:
    * VM host-01: disk kali.qc2: backing file /phenix/images/ro_kali.qc2 is missing
```

Restore the file that the error names on the head node, or point the image at
a copy of it with `Rebase` and `Change reference only` on the `Disks` page (or
minimega's `disk set-backing`). A rebase without that option has to read the
missing file, so it fails. You can also change the topology to use a different
image. If the file is only on a compute node, set
`experiment.disk-check` to `warn` (see
[Disk Check Before a Start](experiments.md#disk-check-before-a-start)).

### A Start Fails on an Image Format

phēnix refuses an image that qemu can't open, such as one with the old AES or
LUKS encryption, a qcow2 version later than 3, or a corrupt qcow2 image that
qemu must write to.
It warns, but starts the VM, for a qcow version 1, vmdk or qed image, or one
that qemu didn't close cleanly. See
[Image Format Versions](disks.md#image-format-versions) for each format.

Stop each VM that uses an image before you change it. Then:

* Upgrade a qcow2 version 2 image to version 3, in place:

    ```bash
    qemu-img amend -f qcow2 -o compat=1.1 kali.qc2
    ```

* Convert a qcow version 1 image to qcow2, then point each image that it backs
  at the copy:

    ```bash
    qemu-img convert -f qcow -O qcow2 kali.qcow kali.qc2
    qemu-img rebase -u -F qcow2 -b kali.qc2 overlay.qc2
    ```

* Remove the old AES encryption, or LUKS, by writing a copy without it. Put
  only the passphrase in a file, with no newline at the end (use
  `driver=qcow` for a qcow version 1 image):

    ```bash
    printf '%s' 'kalipass' > passphrase.txt
    qemu-img convert --object secret,id=sec0,file=passphrase.txt \
      --image-opts driver=qcow2,file.filename=kali.qc2,encrypt.key-secret=sec0 \
      -O qcow2 kali-plain.qc2
    ```

    !!! warning
        The old AES encryption doesn't check the passphrase, so a wrong
        passphrase, or a newline after it, gives you a copy full of random
        data. Examine the copy before you use it.

* Back up and repair an image that qemu marked as corrupt or didn't close
  cleanly:

    ```bash
    cp kali.qc2 kali-backup.qc2
    qemu-img check -r all kali.qc2
    ```

* For an image that a newer qemu made and an older qemu can't open, write a
  copy with the newer `qemu-img`. The copy has no zstd compression, extended
  L2 entries, or external data file:

    ```bash
    qemu-img convert -O qcow2 -o compat=1.1 kali.qc2 kali-compat.qc2
    ```

### phēnix Cannot Reach minimega or Its Images

When minimega doesn't give its version on `<base-dir.minimega>/minimega`,
`phenix doctor` names the likely cause: no socket (minimega isn't running, or
`base-dir.minimega` names another directory), a stale socket file, or a user
that isn't allowed to connect.

phēnix also needs to see minimega's `-filepath` directory to find the images
minimega launches. In a container, mount that directory at the same path for
both, as the Docker Compose setup does with `/phenix`.

### A Start Fails Because an Inject Source Does Not Exist

minimega `disk inject` copies each injection source into the VM's snapshot, so
a missing source fails the start with `cp: cannot stat`. phēnix looks for a
relative source in the experiment's `baseDir`. Put the file there, or change
the injection's `src`. In a container, mount the path at the same location for
phēnix and minimega.

### Injects Fail With an nbd Error

To write injects (at a start, and for `phenix image inject-miniexe`), minimega
`disk inject` connects the image to a `/dev/nbdN` device with `qemu-nbd` and
mounts a partition. These errors come from that step:

| Error | Cause |
| :--- | :--- |
| `add module 'nbd'` | The `nbd` module isn't loaded, or it's part of the kernel. |
| `no available nbds found` | Each `/dev/nbdN` has a lock file in `/var/lock` or a process in `/sys/block/nbdN/pid`. |
| `no partitions found on image` or `desired partition N not found` | The `nbd` module has `max_part` set to 0, or the image has no such partition (see [Injects Go to the Wrong Partition](#injects-go-to-the-wrong-partition)). |

To fix them, as root:

1. Load the module with partitions: `modprobe nbd max_part=10`. If it's
   already loaded with `max_part=0`, disconnect each device, then run
   `modprobe -r nbd && modprobe nbd max_part=10`.
2. For a device a failed inject left behind, unmount its partition with
   `umount <mount-point>` (in `<base-dir.minimega>`, such as
   `/tmp/minimega/dstImg123`), then run `qemu-nbd -d /dev/nbdN`.
3. Remove any lock file `/var/lock/qemu-nbd-nbdN` that no `qemu-nbd` uses.

If `modprobe` can't find the module after a kernel upgrade, reboot into the
installed kernel. In a container, run it privileged with `/dev` from the host,
as the Docker Compose setup does.

### Injects Go to the Wrong Partition

minimega `disk inject` writes the injects, and the `startup` app's scripts,
into partition `inject_partition` of the VM's first drive (1 if unset), using
the number Linux gives it: 1 to 4 for MBR primary, 5 and up for logical, the
entry number for GPT. A wrong number fails the start:

| Error | Cause |
| :--- | :--- |
| `desired partition N not found` | The disk has no partition of that number. |
| `no partitions found on image` | The disk has no partition table or no partition 1, or the `nbd` module has `max_part` set to 0 (see [Injects Fail With an nbd Error](#injects-fail-with-an-nbd-error)). |
| A `mount` error, such as `wrong fs type` | The partition is extended, or holds swap, an LVM physical volume or a LUKS volume. |
| `cp: cannot create regular file '/tmp/minimega/dstImg.../phenix/startup/20-startup.ps1': No such file or directory` | It isn't the system partition. On Windows, partition 1 is often `System Reserved` and `C:` is partition 2 ([sandialabs/sceptre-phenix#166](https://github.com/sandialabs/sceptre-phenix/issues/166)). |

`phenix doctor` suggests a partition for each image, and the `Disks` page
shows an image's partitions (see [The Details Card](disks.md#the-details-card)).
Set `inject_partition` on the VM's first drive to the partition that holds its
system files:

```yaml title="inject into partition 2"
spec:
  nodes:
    - type: VirtualMachine
      general:
        hostname: win-00
      hardware:
        os_type: windows
        drives:
          - image: win10.qc2
            inject_partition: 2
```

If the image has no partition that minimega can write to, such as an LVM
image, set `inject_partition: 0`. The `startup` app then sends its scripts
through miniccc (see
[C2 Startup Script Delivery](apps.md#c2-startup-script-delivery)), and phēnix
injects no other files.

### Open vSwitch Does Not Answer

minimega uses `ovs-vsctl` to make the bridges and taps for VMs. Run
`phenix doctor` as root, like minimega: it fails when `ovs-vsctl` isn't on
`PATH` or `ovsdb-server` doesn't answer, and warns when `ovs-vswitchd` isn't
running, which stops traffic on the bridges. On Debian or Ubuntu, install the
`openvswitch-switch` package, then:

```bash
modprobe openvswitch
systemctl restart openvswitch-switch
```

Don't look for the experiment bridge (`phenix` by default) too early: minimega
makes it when it launches the first VM on it.

### Low Disk Space and Leftovers of Deleted Experiments

`phenix doctor` warns when the filesystem of the images directory, minimega's
`-base` or the minimega log has less than 10% or 5 GiB free, and names what
phēnix and minimega left behind. Run `phenix doctor -p -x` on the head node as root, with
the same `store.endpoint` as `phenix ui`, and work through its rows:

| Row | What to do |
| :--- | :--- |
| leftovers snapshots | `rm <file>` (no stored experiment uses it) |
| leftovers files | Copy out what you want to keep, such as captures, then `rm -r <directory>` |
| leftovers mounts | `umount <path>` |
| leftovers taps | `ovs-vsctl del-port <bridge> <port>`, then `ip link delete <tap>` |
| leftovers mirrors | `ovs-vsctl -- --id=@m get mirror <mirror> -- remove bridge <bridge> mirrors @m` |
| disk-space inject dirs | If no inject is running, unmount each mounted `dstImg*` directory, disconnect its nbd device, then `rmdir <directory>` |

A large minimega log can fill the disk too. Rotate or delete it, for example
with `logrotate` and its `copytruncate` option.

### A VM Does Not Start: `Failed to get "write" lock`

qemu locks each image it opens, so a VM won't start when another process holds
its image or a backing file. `phenix doctor` names each `qemu-system`,
`qemu-nbd` or `qemu-img` process that holds an image of a stopped experiment.
Stop it with `kill <pid>`, or disconnect a `qemu-nbd` device with
`qemu-nbd -d /dev/nbdN`.

A related trap: a VM with `snapshot: false` writes to its drive images.
`phenix doctor` warns when such an image backs another image (the writes break
it), or when more than one VM uses it. Set `snapshot: true`, or give each VM
its own copy of the image, for example with
`qemu-img convert -O qcow2 <image> <copy>`.

### Experiments Show as Running After minimega Restarts

When minimega restarts or stops, the store still marks its experiments as
running. Reset the stored state, then start the experiment again:

```shell
phenix experiment stop my-exp
phenix experiment start my-exp
```

`phenix doctor` also names each minimega namespace with VMs that no stored
experiment owns. Make sure it reads the store of `phenix ui`, then kill those
VMs with `minimega -e 'clear namespace <namespace>'`.

### Apps or a SCORCH Run Stay Marked as Running

If `phenix ui` stops in the middle of an app stage or a SCORCH run, the store
keeps the app marked as running, so phēnix skips that app's running stage and
SCORCH refuses a new run. Start or restart `phenix ui` to clear the marks of
the running experiments (it also resumes their delayed VM starts). If
`phenix doctor` names an interrupted start, stop the experiment with
`phenix experiment stop <experiment>` and start it again.

### An Experiment Cannot Get Its VLANs

minimega refuses a VLAN alias for a VLAN that's in use, and stops when its VLAN
range has no free VLAN. `phenix doctor` finds both before a start:

* Two experiments pin the same VLAN ID: give one of them another ID with
  `phenix vlan alias <experiment> <alias> <id> --force`.
* A stopped experiment's VLAN range has too few free VLANs, or overlaps
  another namespace's range: stop other experiments, or set a larger range
  with `phenix vlan range <experiment> <min> <max> --force`.

The [VLANs](vlans.md) page shows the same conflicts for the whole cluster.

### VMs on Different Nodes Cannot Reach Each Other

In a mesh of more than one node, each experiment bridge needs a trunk port or a
tunnel to the other nodes. Add a trunk on each node, on the interface that
connects the nodes (such as `eth1`):

```shell
minimega -e 'bridge trunk <bridge> <interface>'
```

Or set `use-gre-mesh` to `true`, and phēnix makes GRE tunnels between the
nodes at each start (see [GRE Mesh](gre-mesh.md)).

If `phenix doctor` names a VM scheduled on a node outside minimega's mesh,
change the schedule with `phenix experiment edit <experiment>`, or connect the
node to the mesh.

### minimega Runs Out of Open Files or Memory

minimega keeps about 5 files open for each VM, and each qemu gets minimega's
limits. When `phenix doctor` warns that the soft limit of open files is too
low, raise it, then restart minimega:

* systemd: set `LimitNOFILE=<n>` in the `[Service]` section of the minimega
  unit.
* Docker: add `--ulimit nofile=<n>:<n>` to `docker run`, or `ulimits` to the
  Compose service.

When `phenix ui` has more than 80% of its own limit open, raise it the same way
and restart `phenix ui`. When the host is short of memory or swap, or the VMs
ask for more memory than it has, stop VMs or give them less memory in their
topologies.

### A VM Has No miniccc Client

phēnix needs a miniccc client in a VM when the `startup` app sends its scripts
through miniccc, another VM waits for the client at the start, or the `soh`
app checks the VM. If `phenix doctor` names a VM with no client or a client
whose version differs from minimega's, copy the miniccc of the same minimega
release into the image, and start it when the VM boots:

```shell
phenix image inject-miniexe <miniccc> <image>
```

### `phenix image build` Fails Before or in debootstrap

The build needs `debootstrap`, `kpartx`, `losetup` and `qemu-img` on `PATH`,
loop devices it can use (and a free one), and the right to mount. Run it as
root, or in a privileged container with `/dev` from the host.

`debootstrap` fails with `No such script` when it has no script for the
release, and can't check the Release signature without the keyring the script
names. Install the keyring package (`debian-archive-keyring`, `ubuntu-keyring`
or `kali-archive-keyring`), and for a new release, a later `debootstrap` or a
script in `/usr/share/debootstrap/scripts`. For Kali on a host that isn't
Kali, see the Kali host setup in the `phenix-image` skill
(`skills/phenix-image/references/environment.md`).

Each overlay directory of a stored image config must exist, because `vmdb2`
copies it into the image. Create the directory, or remove it from the config
with `phenix image remove <config> --overlays <path>`.

## See Also

* [Doctor](doctor.md): checks for many of these problems with one command.
* [Logging](logging.md): where the logs are and how to read them.
* [Health Checks](health.md): check a running server.
* [Builder Troubleshooting](builder/troubleshooting.md): problems with the Builder.
