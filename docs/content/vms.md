# VMs

## VM Info

### From the Web-UI

The experiment must be started; click on the experiment name to enter the
Running Experiment component. Within that component, click on the VM name (its
tooltip reads `details and actions`) and you will be presented with the VM's
details card. The URL `/experiment/<name>?vm=<vm>` opens the details card of
that VM.

![VM details for the running hmi-01, with its Windows OS box, power buttons, its screen, resource tiles, storage, a capturing interface, labels, annotations, snapshots and port forwards.](images/vm_details_card.png){: width=800 .center}

#### The Header

The header is colored by the VM's state and shows:

* the VM name and a state tag: `Running`, `Paused`, `Shut down`, `Building`,
  `Delayed start`, `Error`, `Busy` or `Not booted`
* an OS box that names the guest operating system from the topology's
  `hardware.os_type`, with an icon for its family
* a `do not boot` tag, when the VM is flagged not to boot
* the host the VM runs on and how long it has been up, or
  `starts after <delay>` for a VM with a delayed start
* the VM's description from the topology, if it has one
* buttons that open the experiment's [State of Health](state-of-health.md)
  page, its [SCORCH](scorch.md) page, and this documentation page

#### Screenshot and Console

Click the screenshot at the top left of the card to open the VM's console in
a new browser tab. When the VM is not running, the tile says so instead, such
as `No screen: the VM is shut down`.

#### Power

The `Power` section holds the VM's power buttons:

* `Start`: start the VM. On a paused VM the button reads `Resume`.
* `Pause`: pause the VM until it is resumed.
* `Restart`: restart the running VM.
* `Shut down`: power off the VM. It can be started again later.
* `Kill`: kill the VM and remove it from the running experiment.

`Kill` asks you to confirm first. The other buttons act at once, as a
[background job](jobs.md), and the card then shows the VM as the action left
it.

#### More Actions

`More actions`, under the screenshot, holds everything else:

| Button | What it does |
| ------ | ------------ |
| `Redeploy` | Kill the VM and deploy it again, optionally with new CPUs, memory or disk |
| `Reset disk` | Reset the VM's disk to how it was when the experiment started, discarding changes |
| `Snapshot` | Save the VM's current disk and memory as a snapshot that can be restored later |
| `Backing image` | Create a new backing image from the current VM disk |
| `Memory snapshot` | Save a dump of the VM's memory for forensics tools such as Volatility |
| `Port forward` | Forward a port to a port on the VM through its miniccc agent |
| `Insert CD-ROM` | Insert an ISO image into the VM's CD-ROM drive. Reads `Change CD-ROM` when one is inserted |
| `Mount disk` | Mount the VM's filesystem to browse, upload and download files |

`Reset disk` and `Snapshot` ask you to confirm first, and the other buttons
open a dialog. `Mount disk` appears only when the `vm-mount` feature is
enabled (see [Mount a VM](#mount-a-vm)). An action your role may not take is
hidden, and one the VM's state rules out is disabled with the reason in its
tooltip, such as `Cannot pause: the VM is not running`.

![VM details for the shut-down historian, with Start enabled and Restart and the other actions a stopped VM cannot take grayed out.](images/vm_details_disabled.png){: width=800 .center}

#### Tiles

Four tiles beside the screenshot show the VM's `miniccc agent` state,
`vCPUs`, `Memory` and `Interfaces`.

#### Storage

`Storage` names the VM's disk, tagged `snapshot on` or `snapshot off`. With
`snapshot on`, writes go to a temporary overlay and the disk image is left
unchanged. The disk is named as the [Disks](disks.md) page names it, with the
same icons for an image
[outside the files directory](disks.md#images-outside-the-files-directory) or
with a [broken backing chain](disks.md#broken-backing-chains). Its backing
images are listed beneath it. A `CD-ROM` row appears while an ISO image is
inserted. If your role may list disks, each name opens the image's
[details card](disks.md#the-details-card) on the Disks page.

#### Network

`Network` lists one row per interface: its index, VLAN alias, VLAN ID, IP
address and tap name. Click the alias to move the interface to another VLAN,
if your role may do so. While the VM runs, each connected interface has a
shark-fin button that starts a packet capture on it. A capturing row is
highlighted, its button becomes a red stop button, and an `Open in WebShark`
button appears beside it. The section heading holds `Capture all interfaces`,
`Stop all packet captures` and a button that opens the
[WebShark](webshark.md) tab. See [Packet Capture](#packet-capture) and
[Capture All Interfaces](#capture-all-interfaces).

#### Labels, Snapshots and Port Forwards

`Labels` lists the VM's labels, and `view/edit` opens the label editor.
`Snapshots` lists the VM's snapshots, and the play button next to a snapshot
restores it after you confirm. `Port forwards` lists each forward with the
user who created it, `add` creates one, and you can delete the forwards you
own. Each section appears only if your role may list what it holds.

#### Annotations

`Annotations` lists the VM's node annotations, which apps read for per-VM
settings, from the experiment's copy of the topology. If your role may update
the VM, `edit` opens the same editor the create experiment card uses (see
[Adding Annotations](experiments.md#adding-annotations)), and `Save` replaces
the VM's annotations with the list. Changes are saved to the experiment, not
to the Topology config. Apps read most annotations when the experiment
starts, so most changes take effect the next time it starts.

#### VM State Unknown

When minimega does not answer for every VM of a running experiment, the `VMs`
tab says `minimega did not answer. Showing the VMs as configured`. The VMs
minimega did not report are listed as the topology configures them, with no
state, until minimega answers.

#### Busy VMs

While an action runs on the VM, the card shows `Busy with another action` and
disables every action. Clicking a busy VM's name in the table opens a
`VM Busy` alert instead of the card.

### From the Command Line Binary

There are two options for displaying the information for VMs in an experiment. First run
the following command to see information for all VMs in a given experiment.

```shell
phenix vm info <experiment name>
```

Or, run the following to see the information for a specific VM in an experiment.

```shell
phenix vm info <experiment name> [vm name] [--label <label>] [--host <host>]
```

`--host` lists only the VMs running on that cluster host. Repeat it to list
the VMs on any of several hosts, and combine it with a VM name, `--label` or
`--filter`. Press Tab after `--host` to complete host names.

```shell
phenix vm info my-exp --host compute1 --host compute2
phenix vm info my-exp --host compute1 --label role-web
```

## Filtering and Sorting the VM Table

On the running experiment's `VMs` tab, the `Hosts`, `Labels` and `Paginate`
controls sit to the left of the `Find a VM` search box.

* `Hosts` lists every cluster host running one of the experiment's VMs. Check
  hosts to list only the VMs running on any of them.
* `Labels` lists every `key=value` label on the experiment's VMs. Check
  labels to list only the VMs that carry any of them.

Each button shows how many items are checked, and `Clear hosts` or
`Clear labels` unchecks them all. The filters combine with each other and
with the search box: a VM must match all of them.

Click the `Labels` heading to sort the VMs by how many labels they have, the
`Network` heading to sort them by the VLAN IDs of their interfaces, or the
`Taps` heading to sort them by tap name. VMs with no VLAN ID or tap, such as
VMs that are not running, come last. The `Uptime` column shows a dash for a
VM that is not running and `unknown` for an external node.

The same filters and sort columns are available through the [API](api.md).

## VM Actions Column

The running experiment's VM table has an `Actions` column of icon buttons
that act on that row's VM without opening its details card:

* `Shut down`, which becomes `Start` (or `Resume` for a paused VM) while the
  VM is not running
* `Restart`
* `Port forward`
* `Snapshot`
* `Backing image`
* `Capture all interfaces`, which becomes `Stop all packet captures` while the
  VM is capturing (see [Capture All Interfaces](#capture-all-interfaces))

![VM table with an external node that has no buttons, a shut-down historian whose Actions column shows Start with Restart grayed out and whose Uptime is a dash, and a tooltip explaining that a VM without snapshots enabled cannot be snapshotted.](images/vm_row_actions.png){: width=800 .center}

The buttons work as they do on the details card: `Snapshot` asks you to
confirm, `Port forward` and `Backing image` open their dialogs, and the rest
act at once. A disabled button gives the reason in its tooltip, such as
`Cannot snapshot: you do not have permission`. External nodes have no
buttons, and the column is hidden if your role may take none of these
actions. You can turn the column off from the column picker above the table.

## Create a Backing Image

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Backing image` in `More actions`. The same action is in the
VM table's [Actions column](#vm-actions-column). The VM must be running and
have snapshots enabled.

phēnix shuts down the VM while it makes the image, and then starts the VM
again. It also starts the VM again if the operation fails.

### From the Command Line Binary

To create a backing image from the first disk of a running VM, run the
following command.

```shell
phenix vm commit <experiment name> <vm name> [image name]
```

The image name must be a bare file name, with no folder. It must not start
with a scheme that minimega fetches, such as `file:` or `http:`. If you do not
give a name, phēnix names the image after the base image of the VM and the time.
phēnix writes the image in `<base-dir.phenix>/images` on the head node, and
copies it to the other cluster nodes. The command prints the name of the
image. The VM can run on any cluster node.

The VM must have its snapshot flag set. For a VM with more than one disk, the
image holds only the first disk, and the command shows a warning.

## Create a Memory Snapshot

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Memory snapshot` in `More actions`. The button is disabled
when the VM is not running.

### From the Command Line Binary

To create an ELF memory dump, run the following command.

```shell
phenix vm memory-snapshot <experiment name> <vm name> <snapshot file path>
```

A relative path is in the experiment files directory,
`<base-dir.phenix>/images/<experiment name>/files`. For a VM on a compute
node, phēnix copies the dump to the head node from that directory only. Thus,
for such a VM, phēnix refuses an absolute path outside that directory. Each
memory snapshot stops after one hour if QEMU does not complete it.

## Create a VM Snapshot

### From the Web-UI

Click on a running VM in a started experiment to open its details card, then
click `Snapshot` in `More actions` and confirm. The same action is in the VM
table's [Actions column](#vm-actions-column). The VM must be running and have
snapshots enabled. Once a snapshot exists, the card's `Snapshots` section
lists it, and the play button next to its name restores it.

### From the Command Line Binary

A VM snapshot holds the disk and the memory state of a VM. For an ELF memory
dump, use `phenix vm memory-snapshot`. To save and restore the state of a
running VM, do the steps that follow.

1. Create a snapshot of the VM. The command prints the name of the snapshot,
    for example `web__before-update`.

    ```shell
    phenix vm snapshot create <experiment name> <vm name> <snapshot name>
    ```

2. List the snapshots of the VM.

    ```shell
    phenix vm snapshot list <experiment name> <vm name>
    ```

3. Restore the VM to a snapshot. Use a name from the list.

    ```shell
    phenix vm snapshot restore <experiment name> <vm name> <snapshot name>
    ```

The VM must have its snapshot flag set and only one disk. phēnix refuses a VM
with more than one disk, because a snapshot holds only the first disk
([#145](https://github.com/sandialabs/sceptre-phenix/issues/145)).

phēnix keeps the snapshot files on the cluster node that runs the VM, in
`<base-dir.phenix>/images/<experiment name>/files`. The list shows the
snapshots on all nodes. A restore of a VM on a node other than the head node
can fail if minimega on that node runs with the `-headnode` flag. minimega
then gets the snapshot files from the head node, which does not have them
([#153](https://github.com/sandialabs/sceptre-phenix/issues/153)).

## VM VNC Access

### From the Web-UI

The experiment must be started; click on the VM screenshot to open a new browser
tab that provides VNC access to the VM.

#### VNC Banners

The VNC tab shows a banner above and below the VM's screen. Both read
`EXP: <experiment> - VM: <vm>` unless the VM has a `vncBanner` annotation:

* A string sets the text of both banners. Each newline in it starts a new
  line.
* A map sets each banner apart. `topBanner` and `bottomBanner` each take a
  `banner` list of lines, a `backgroundColor` and a `textColor`. A banner the
  map leaves out is not shown, and `disabled: true` turns both off.

```yaml
annotations:
  vncBanner:
    topBanner:
      banner:
        - Training range
        - Lab use only
      backgroundColor: "#007a33"
      textColor: white
    bottomBanner:
      banner:
        - Training range
      backgroundColor: "#007a33"
      textColor: white
```

Colors are CSS color names, such as `green`, or hex colors, such as `#007a33`.
Any other color falls back to the default white background or black text.
Banner text is shown exactly as written, so HTML in it appears as text.

### From the Command Line Binary

Not applicable.

## Mount a VM

The VM Mount feature lets you transfer files to and from a running VM directly
through the phēnix Web-UI. It is an optional feature that must be enabled when
starting the phēnix UI with the `vm-mount` feature flag (see
[Enabling VM Mount](#enabling-vm-mount) below).

!!! note

    Mounting a VM requires the minimega command-and-control agent (`miniccc`)
    to be installed and actively running in the VM. If `cc` is not active for a
    VM, the `Mount disk` button is disabled and `phenix vm mount` fails.

### Enabling VM Mount

VM Mount is disabled by default. Enable it by passing the `vm-mount` feature to
the `--features` flag when starting the UI:

```shell
phenix ui --features vm-mount
```

The feature can also be enabled via the `ui.features` configuration key or the
`PHENIX_UI_FEATURES` environment variable. See
[Settings & Configuration](settings.md#settings-reference) for details.

### From the Web-UI

The experiment must be started. Click on the name of a running VM to open its
details card, then click `Mount disk` (the hard-drive icon) in `More actions`.
The button is disabled, with the reason in its tooltip, when the VM is not
running or has no active miniccc agent.

The mount browser modal presents two panes:

* **Local file system** — browse to a file on your local machine and upload it
  into the VM, or download a file from the VM to your local machine. An
  interrupted download resumes when retried from the browser's downloads list.
* **Experiment files** — copy files that already exist on the phēnix server
  into the VM. This option is only available when the
  [experiment file server](settings.md#settings-reference) is enabled (see
  below).

Every path stays inside the VM's file system, so a symbolic link that points
outside it cannot be browsed or written through. Several users can have the
same VM mounted at once, and it is unmounted when the last of them closes the
modal.

### Uploading Experiment Files from the phēnix Server { #uploading-experiment-files-from-the-phenix-server }

phēnix can optionally run a lightweight file server on a port separate from the
main UI. This was added to support environments where the phēnix UI is only
reachable on a private network but files (for example, application installers or
configuration files) need to be uploaded to an experiment from a public network.

When the file server is enabled, the VM Mount modal gains the **Experiment
files** option described above, allowing files that have been uploaded to an
experiment to be copied directly into a VM without going through the user's
local machine.

Enable the file server by passing the `--file-server-endpoint` flag when
starting the UI:

```shell
# Bind to 127.0.0.1 on port 8080 (port-only value binds to localhost)
phenix ui --features vm-mount --file-server-endpoint 8080

# Bind to a specific interface and port
phenix ui --features vm-mount --file-server-endpoint 0.0.0.0:8080
```

The file server presents a simple interface for selecting an experiment and
uploading a file to it. When authentication/authorization is enabled for the
main phēnix UI, it is automatically enforced on the file server as well, so
users must already have valid phēnix credentials to upload files. File upload
permissions are included in the default **Experiment Admin** and **Experiment
User** roles.

See [Settings & Configuration](settings.md#settings-reference) for the
`ui.file-server-endpoint` configuration key.

### From the Command Line Binary

Use `phenix vm mount` to mount a running VM's filesystem to a directory on the
phēnix headnode. Unlike the Web-UI mount browser above, this does not require
the `vm-mount` UI feature flag to be enabled, and the mounted filesystem is
accessed directly on the headnode's filesystem rather than through the
browser.

```shell
phenix vm mount <experiment name> <vm name> [host path]
```

If `host path` is omitted, the VM is mounted to a default path of
`<mount-dir>/<experiment name>/<vm name>`, where `<mount-dir>` defaults to
`<base-dir.phenix>/mounts` (see [`mount-dir`](settings.md#settings-reference)).
The command prints the resolved host path once the mount succeeds.

```shell
phenix vm mount my-exp my-vm
# VM my-vm in experiment my-exp mounted at: /phenix/mounts/my-exp/my-vm

phenix vm mount my-exp my-vm /tmp/my-vm-files
# VM my-vm in experiment my-exp mounted at: /tmp/my-vm-files
```

To unmount the VM's filesystem, run:

```shell
phenix vm unmount <experiment name> <vm name>
```

!!! note

    Mounts are automatically unmounted, and their mount directories removed,
    when the owning experiment is stopped or deleted, so mounts do not
    outlive their experiment.

## Packet Capture

### From the Web-UI

Click on the name of the network tap, or on the IP address, of a running VM in a
started experiment to start a packet capture on that interface. The name of the
network tap will turn green once a packet capture has started. Each interface in
the VM's details card has its own shark-fin button that does the same thing.

phēnix starts the capture, or stops a running one, without asking you to
confirm. A capture started from the web UI is written to the experiment's
files as `<vm>_<interface>_<time>_<random>.pcap`, so a restarted capture
never replaces an earlier file.

It is possible to start captures on multiple network taps. Stopping the
capture on one interface leaves the VM's other captures running. Use
`Stop all packet captures` to stop them all.

A capture cannot be started on a disconnected interface; phēnix says
`Cannot capture traffic on a disconnected interface.` instead of starting one.

### From the Command Line Binary

To start a packet capture, run the following command, specifying the target
network interface by its name (as declared in the experiment topology, e.g.
`IF0`) or its zero-based index.

```shell
phenix vm capture start <experiment name> <vm name> <iface name/index> </path/to/out file>
```

To stop all packet captures on a running VM, use the following command.

```shell
phenix vm capture stop <experiment name> <vm name>
```

Pass an interface name or index to stop only that interface's capture, leaving
the VM's other captures running.

```shell
phenix vm capture stop <experiment name> <vm name> [iface name/index]
```

!!! note
    Stopping the capture on a single interface, from the Web-UI or the command
    line, requires minimega 3.3.0 or later.

To view a capture's packets in the browser, or stream a running capture into a
local Wireshark (`phenix vm capture stream`), see [WebShark](webshark.md).

## Capture All Interfaces

`Capture all interfaces`, in the VM table's
[Actions column](#vm-actions-column) and in the details card's
[Network](#network) section, starts a packet capture on every connected
interface of the VM that does not already have one. It does not ask you to
confirm, and writes one `.pcap` file per interface to the experiment's files.
The button is disabled, with the reason in its tooltip, when the VM is not
running, has no connected interface, or is already capturing every connected
interface. While captures run, a red `Stop all packet captures` button takes
its place and stops them all.

## Kill a VM

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Kill` in the `Power` section and confirm.

_Note_: if you stop and then start the experiment again, that VM will run again
per the experiment configuration.

### From the Command Line Binary

To kill a VM, run the following command.

```shell
phenix vm kill <experiment name> [vm name] [--label <label>]
```

## Modify the Network Connectivity

### From the Web-UI

Click on the network for the desired VM in the Running Component to modify the
settings. Select from a pull down what network you want to switch the VM interface
you clicked on to. To revert back to previous setting, simply repeat selecting the
network interface you wish to change, and select the previous network setting.

### From the Command Line Binary

To connect a VM network interface to a different network, run the following command.

```shell
phenix vm net connect <experiment name> <vm name> <iface index> <vlan id>
```

To disconnect a VM network interface, run the following command.

```shell
phenix vm net disconnect <experiment name> <vm name> <iface index>
```

## Pause a VM

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Pause` in the `Power` section.

### From the Command Line Binary

To pause a VM, run the following command.

```shell
phenix vm pause <experiment name> [vm name] [--label <label>]
```

To resume a paused VM, run the following command.

```shell
phenix vm resume <experiment name> [vm name] [--label <label>]
```

## Redeploy a VM

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Redeploy` in `More actions`. The dialog that opens lets you
change the VM's CPUs, memory and disk, and whether the original injections are
replicated, before it is deployed again. Its `Disk` menu works as described in
[Disk Menu](#disk-menu), and the disk changes only when you pick a different
one.

### From the Command Line Binary

To redeploy a VM, run the following command.

```shell
phenix vm redeploy <experiment name> [vm name] [--label <label>]
```

The `-c, --cpu` and `-m, --mem` flags change a value only when you set them.
Without them, the VM keeps its current CPUs and memory. A new disk (`-d, --disk`)
replaces only the first disk of the VM. The other disks do not change.
The disk value is a minimega disk spec, `<path>[,<interface>][,<cache mode>]`,
for example `new.qc2,virtio`. A field that you do not set keeps the value of
the current first disk.

## Reset Disk State

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Reset disk` in `More actions` and confirm. The button is
disabled when the VM is paused or does not have snapshots enabled, with the
reason in its tooltip, such as `Cannot reset disk: the VM is paused. Resume
paused VMs first`.

### From the Command Line Binary

To reset the first disk to the initial pre-boot state, run the following command.

```shell
phenix vm reset-disk <experiment name> [vm name] [--label <label>]
```

## Restart a VM

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Restart` in the `Power` section. `Restart` is also in the VM
table's [Actions column](#vm-actions-column). The button is disabled when the
VM is not running, with the reason in its tooltip. Restarting a selection of
VMs from the table's toolbar skips the ones that are not running.

### From the Command Line Binary

To restart a VM, run the following command.

```shell
phenix vm restart <experiment name> [vm name] [--label <label>]
```

## Resume a VM

### From the Web-UI

Click on the name of the paused VM in a started experiment to open its details
card, then click `Resume` in the `Power` section.

phēnix refuses to start a VM that the topology sets to not boot
(`general.do_not_boot`), because minimega did not launch it. To launch the VM,
redeploy it.

### From the Command Line Binary

To resume a paused VM, run the following command.

```shell
phenix vm resume <experiment name> [vm name] [--label <label>]
```

## Shutdown a VM

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Shut down` in the `Power` section. `Shut down` is also in the
VM table's [Actions column](#vm-actions-column).

### From the Command Line Binary

To shutdown a VM, run the following command.

```shell
phenix vm shutdown <experiment name> [vm name] [--label <label>]
```

## Modify VM Settings

### From the Web-UI

There are two ways to modify VM settings:

1. Click on a stopped experiment to access the Stopped Component. Its VM table
shows each VM's host and IP address, and lets you edit the following:
    * Host: the cluster host the VM is scheduled on
    * CPUs
    * Memory
    * Disk: the image the VM boots from (see [Disk Menu](#disk-menu))
    * Partition: the disk partition files are injected into
    * Labels
    * Boot: the do-not-boot flag
    * Persistence: `Non-Persistent` (snapshots on) or `Persistent`

    Edit a setting in the row of the VM, or click the VM name to open its
    details card. The card has the same settings, and you can also edit the
    labels and annotations of the VM there. A change saves at once, with no
    dialog. The control spins until the server answers. If the change fails,
    an error message shows and the control shows the previous value. A dot
    next to a setting tells that its value is different from the value that
    the VM had when the experiment was created. `Reset settings` sets all of
    the settings back to those values. Experiments created before this
    version of phēnix have no saved values, so they do not show `Reset
    settings`. The card also lists the port forwards that the
    `phenix/startup-autotunnel` annotation opens when the experiment starts.
    To change them, edit the annotation. The `Select visible columns` button
    shows or hides the columns of the table.
1. From a running experiment, click on the VM name and then `Redeploy` in
`More actions` on its details card. You are able to edit the following:
    * CPUs
    * Memory
    * Disk
    * Replicate Original Injection(s)

### Disk Menu

The stopped experiment's `Disk` column and the `Redeploy` dialog pick a VM's
disk from the images the experiment can use on the [Disks](disks.md) page.
Images in the minimega files directory are listed by their path within it,
such as `win/win10.qcow2`. Images outside that directory are listed by full
path under `Outside the standard images directory`. On a stopped experiment,
picking an image asks you to confirm, then writes the image's full path into
the topology.

* An image whose path minimega cannot use is disabled and marked
  `(minimega cannot use this name)`.
* A warning icon marks a disk
  [outside the files directory](disks.md#images-outside-the-files-directory).
* A red icon with a cross marks a disk whose
  [backing chain is broken](disks.md#broken-backing-chains).
* A disk the list does not hold, such as a missing image, shows as
  `<full path> (not listed)`.

The menu needs `list` on `disks` to offer other images. Without it, the menu
shows only the VM's current disk.

### CD-ROM

`Insert CD-ROM` in the details card's `More actions` opens the CD-ROM picker
for a running VM. When an ISO image is already inserted, the button reads
`Change CD-ROM` and the picker names the inserted image. The picker lists the
ISO images the [Disks](disks.md) page knows, with those outside the minimega
files directory in a group of their own. If there are none, it says so and
links to the Disks page, where an `.iso` file can be uploaded. `Insert` (or
`Change`) inserts the selected image, and `Eject` removes the inserted one.

![Change the CD-ROM dialog for site-1-hmi showing the inserted ISO and an ISO image drop-down, with Cancel, Eject and Change buttons.](images/vm_cdrom_picker.png){: width=500 .center}

### From the Command Line Binary

To modify VM settings, run the following command, specifying one or more of the
configuration flags described below.

```shell
phenix vm set <experiment name> [<vm name>] [flags]
```

Only the flags that are explicitly provided will be applied; any setting whose
flag is omitted is left unchanged. The following flags are supported:

| Flag                  | Description                                                                                                       |
| --------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `-c, --cpu`           | Number of VM CPUs (1-8 is valid).                                                                                 |
| `-m, --mem`           | Amount of memory in megabytes (a multiple of 512 from 512 to 16384).                                              |
| `-d, --disk`          | VM backing disk image.                                                                                            |
| `-p, --partition`     | Partition of disk to inject files into.                                                                           |
| `--do-not-boot`       | Set the do-not-boot flag for the VM.                                                                              |
| `--snapshot`          | Set the snapshot (non-persistent) flag for the VM.                                                                |
| `-L, --label-changes key=value` | VM label to set, in `key=value` form. May be repeated to set multiple labels.                                         |
| `--append-labels`      | Append the provided labels to the VM's existing labels instead of replacing them.                                     |
| `-l, --label`         | Apply the change to every VM whose label matches the provided label (supports glob patterns). Use `all` to select every VM in the experiment. May be repeated. |

A label key or value may not hold a double quote, a backslash, `$`, or a
control character other than a newline or tab, and may not start with
`file:`, `http:`, `https:` or `tar:`. A VLAN an interface connects to may not
hold white space, `'` or `#`.

For example, to set the CPU count to 4 and memory to 2048 MB for a VM named
`my-vm` in an experiment named `my-exp`:

```shell
phenix vm set my-exp my-vm --cpu 4 --mem 2048
```

To set the `role=server` labels on VMs whose existing labels matches
`tier=web*` in the same experiment, without clearing the existing labels:

```shell
phenix vm set my-exp --label "tier=web*" -L role=server --append-labels
```

To set two labels on the VM `my-vm` and replace the existing labels on the VM:

```shell
phenix vm set my-exp my-vm -L val1=true -L val2=roger
```

!!! note
    Only label updates and interface VLAN connections can be modified while an
    experiment is running. To change CPU, memory, disk, partition, do-not-boot,
    or snapshot settings, the experiment must be stopped. Use
    [`phenix vm net`](#modify-the-network-connectivity) to modify interface
    VLAN connections on a running experiment.

## Applying Actions to Multiple VMs

!!! note
    See [VM Multi Action](vm-multi-action.md) for documentation on applying
    actions to multiple VMs at once.
