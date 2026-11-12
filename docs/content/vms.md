# VMs

## VM Info

### From the Web-UI

The experiment must be started; click on the experiment name to enter the
Running Experiment component. Within that component, click on the VM name (its
tooltip reads `details and actions`) and you will be presented with the VM's
details card. The URL `/experiment/<name>?vm=<vm>` opens the details card of
that VM.

![VM details for the running hmi-01, with its Windows OS box, power buttons, its screen, resource tiles, storage, a capturing interface, labels, annotations, snapshots and port forwards.](images/vm_details_card.png){: width=800 .center}

Available commands

* `restore` a snapshot by clicking the play button next to the desired snapshot name.

The `Power` section holds the `Start` (or `Resume`), `Pause`, `Restart`,
`Shut down` and `Kill` buttons. `More actions`, under the screenshot, holds
the other actions.

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

## Create a Backing Image

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Backing image` in `More actions`. The button is disabled
when the VM is not running or does not have snapshots enabled.

phēnix shuts down the VM while it makes the image, and then starts the VM
again. It also starts the VM again if the operation fails.

### From the Command Line Binary

There is currently no CLI subcommand to create backing images from running VMs. This operation is available through the Web-UI and the REST API (`POST /api/vms/commit`).

## Create a Memory Snapshot

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Memory snapshot` in `More actions`. Type the file name and
click `Create`, or click `Cancel` to stop. The button is disabled
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
click `Snapshot` in `More actions` and confirm. The button is disabled when
the VM is not running or does not have snapshots enabled. In Live mode, the bar
of a tile in the VNC tab also has `Snapshot` (see [Live Tiles](#live-tiles)).
The VM table's `Actions` column, the selection toolbar and the top bar of the
console page also ask you to confirm.

### From the Command Line Binary

While ELF memory dumps can be created via `phenix vm memory-snapshot`, there is currently no CLI subcommand to take standard VM disk snapshots. VM disk snapshots are managed through the Web-UI and the REST API (`POST /api/vms/snapshots`).

## Save a VM Screenshot

### From the Web-UI

Click on a running VM in a started experiment to open its details card, then
click `Screenshot` (the camera icon) in `More actions`. The console page has
the same button in its top bar. phēnix saves a full-size PNG image of the VM
screen in the experiment files, in the `screenshots` folder. The file name is
`<vm>-<YYYYMMDD-HHMMSS>.png`, with the UTC time. phēnix does not replace a
file: when the name is in use, it adds `-1`, `-2` and so on. A message shows
the name of the file. The button is disabled when the VM is not running.
In Live mode, the bar of a tile in the VNC tab also has `Screenshot`.

The role must have the `create` verb on the `vms/screenshot` resource (see
[User Administration](user-administration.md)).

### From the Command Line Binary

There is currently no CLI subcommand to save a VM screenshot. Use the Web-UI
or the REST API (`POST /api/v1/experiments/<exp>/vms/<vm>/screenshots`).

## VM VNC Access

### From the Web-UI

The experiment must be started. The web UI shows the screen of a VM in three
places:

* The console page (`/vnc`) shows one to four VM consoles in a browser tab.
* The VNC tab of a running experiment and the VM Tiles page show live tiles.
* The details window of a VM shows a live console.

The VM must be running or paused, and it must not be busy with an action. The
role must have the `get` verb on the `vms/vnc` resource (see
[User Administration](user-administration.md)).

!!! note

    The console needs the minimega release that adds the `vga` column to
    `vm info`. With an older minimega, the console shows `minimega is too old:
    vm info has no vga column, update minimega`. Port forwards to the VNC port
    of a VM also fail. Key recordings (`vnc record kb`) of an older minimega
    do not include the key events that noVNC sends.

#### Open a Console

Click the screenshot of a VM in the VMs table, a live tile in the VNC tab, or
`Open console` in the details window of the VM. The console page opens in a
new browser tab. Each link has a real address. You can open the link in a new
window or copy it. When another tab already shows the same VMs, a message
tells you. Click `Open another anyway` to open one more tab.

#### The Console Page

The top bar acts on the pane that has keyboard input. It shows the VM name
and these controls:

* The flask button, at the left of the VM name, opens the page of the
  experiment of the pane.
* In single view, the information button at the right of the VM name opens
  the details card of the VM on the experiment page.
* The VM action buttons: `Pause` or `Resume`, `Restart`, `Shut down`,
  `Screenshot`, `Port forwards`, `Mount disk` and the packet capture button.
  `Screenshot` saves a PNG image of the VM screen in the experiment files
  (see [Save a VM Screenshot](#save-a-vm-screenshot)). `Mount disk` opens
  the same file browser as on the experiment page. It needs the `vm-mount`
  feature, a running VM and an active miniccc agent.
* `Port forwards` lists the port forwards of the VM. Each forward has
  `Remove`. Only the user that made a forward can remove it.
  `Add port forward` opens the same dialog as on the experiment page.
  `Tunneler` opens the [Tunneler](tunneler.md) page in a new tab.
* The packet capture button starts a capture on interface 0 when no capture
  of the VM runs. When minimega can write pcapng, a dialog asks for the
  format first. When a capture of the VM runs, the button is
  `Stop captures` and stops all captures of the VM.
* `Keys`, `Clipboard` and `View` are icon buttons. Their tooltips give their
  names.
* `Keys`: send Ctrl+Alt+Del, Esc, Tab, PrintScreen and Ctrl+Alt+F1 to F12. Hold
  Ctrl, Alt or Win, and release all held keys.
* `Clipboard`: send text to the VM (see [Copy and Paste](#copy-and-paste)).
  `Clear` empties the text box.
* `View`: the scaling (`Fit`, `Actual` or `Resize VM`), the quality, the
  compression, `View only`, full screen for the pane, and the keyboard
  shortcuts (see [The Keyboard Chord](#the-keyboard-chord)). The browser keeps
  these settings for each user. With `View only`, the console shows the VM
  screen, but sends no keyboard or mouse input to the VM.
  `Resize VM` needs a VM with `vga` `virtio`.
  To set it, add `vga: virtio` to the `advanced` settings of the VM in the
  topology. Linux VMs use `qxl` by default.
* `Actions`: a menu with `Snapshot`, `Memory snapshot`, `Backing image` and
  `Capture all interfaces` (or `Stop all packet captures`), then
  `Insert CD-ROM` (or `Change CD-ROM`), then `Edit labels` and
  `Edit annotations`, then `Redeploy`, `Reset disk` and `Kill` in red. They
  open the same dialogs as on the experiment page.
* The layout buttons, `Add VM` and `Full screen` for the page. In full
  screen, the same button is `Exit full screen`. `Add VM` shows the list of
  running VMs that it read the last time, and reads the list again. The
  browser keeps the list for each user.
* The connection state, for example `Connected`. Click it to see the screen
  size, the scaling, the encoding and the copy/paste mode. It also shows the
  host of the VM, its uptime, the state of its miniccc agent and its IP
  addresses, each with a copy button.
* `Hide toolbar`, at the right end, hides the top bar. The panes then use
  all of the page. A small `Show toolbar` button in the top left corner shows
  the bar again. Ctrl+] then T also hides or shows the bar (see
  [The Keyboard Chord](#the-keyboard-chord)). The browser keeps the choice.

An action that the VM state does not allow is grayed out, and its tooltip
gives the reason. An action that the role cannot do is also grayed out. Its
tooltip names the permission, for example
`Your role does not permit this action (vms delete)`.

`Pause`, `Restart`, `Shut down`, `Snapshot`, `Reset disk` and `Kill` ask you
to confirm. `Resume` does not ask. During `Redeploy` and `Reset disk`, the
pane shows that the VM restarts, and the console connects again when the VM
runs. After `Kill`, the pane shows `VM killed` and a link to the experiment
page.

![The console page in two columns: the web pane has keyboard input, and each pane shows its VM between its two banners](images/vnc_console_split.png)

#### Split View

The console page shows up to four VMs. The VMs can be in different
experiments. Use `Add VM` to add a running VM, and select a layout in the top
bar: `One pane`, `Two columns`, `Two rows`, `One large pane and two stacked`
or `Grid of four`. The address of the page keeps the VMs and the layout, for
example `/vnc?vm=demo/web&vm=demo/db&layout=cols`. The layouts are `single`,
`cols`, `rows`, `main-2` and `grid`.

Each pane shows the banners of its VM and a header with the VM name, the VM
state, an information button, the pane number and a menu. The information
button opens the details card of the VM on the experiment page. The menu has `Swap with pane N`, `Open
alone`, `Full screen` and `Close`. Click a pane to give it keyboard input.
That pane shows a blue ring and the `Keyboard` tag. When you close a pane and
only one VM stays, the page shows that VM in one pane.

To open many VMs in one console page, select one to four running VMs in the
VMs tab, or with the check box on each tile in the VNC tab. Then click
`Open N selected in split` in the VNC tab.

#### The Keyboard Chord

The console sends every key to the VM. To move the keyboard out of the
console, press Ctrl+], then one more key:

| Key | Action |
|---|---|
| Tab | Move focus to the top bar. Shift+Tab moves focus to the last control of the bar. When the top bar is hidden, move focus to `Show toolbar`. |
| 1 to 4 | Give keyboard input to that pane. |
| F | Exit full screen. |
| T | Hide or show the top bar. |
| Esc | Cancel the chord. |

Press the key in the position of ] on a US keyboard. On a German keyboard,
that key is +. The `Keyboard` tag, the `Keys` menu and a message show this
hint. The message shows the first time that a pane gets keyboard input in a
browser session.

#### Full Screen and HTTPS

In full screen over HTTPS, Chrome and Edge send Esc, Alt+Tab and Ctrl+W to the
VM (Keyboard Lock). To exit full screen, press Ctrl+] then F, or hold Esc.
Over HTTP, full screen works without Keyboard Lock, and a message says
`Keyboard capture needs HTTPS`. Firefox and Safari do not have Keyboard Lock.
`Paste from clipboard` in the `Clipboard` panel also needs HTTPS.

#### Copy and Paste

The copy/paste mode of a VM comes from its minimega setting
`bidirectional-copy-paste`:

* Paste as keys (the default): phēnix types the text into the VM as key
  presses. It types printable ASCII characters, Tab and Enter only. Before it
  types text with other characters, the panel lists the characters that it
  types as "?" (above U+00FF) and the characters that it skips (U+0080 to
  U+00FF). It also shows the typing time. Click `Type anyway` or `Cancel`.
  For text longer than 5,000 characters, the bidirectional mode is faster.
* Bidirectional: the VM and the `Clipboard` panel exchange UTF-8 text in the
  two directions.

#### Live Tiles

The VNC tab of a running experiment and the VM Tiles page show a live,
view-only tile for each running or paused VM. A tile sends no keys and no
pointer events to the VM. A paused VM keeps a still tile with the `PAUSED`
tag. Click a tile to open its console page. Move the pointer over a tile to
show its bar, with `Open console`, `Details` and the VM power actions. In Live
mode, the bar also has `Screenshot` and `Snapshot`. The bar also shows when a control of the tile has
keyboard focus. A disabled button tells why in its tooltip. `Pause`,
`Restart`, `Shut down`, `Kill` and `Snapshot` ask you to confirm. `Start` and
`Resume` do not ask.

![The VNC tab with the Live and Snapshots switch, the Live tile limit, and live tiles for two running VMs, one paused VM and one VM that is not running](images/vnc_tiles.png)

* `Live tile limit` sets how many tiles can be live at one time: 4, 8, 16 (the
  default), 24 or 32. The browser keeps the choice. A tile over the limit
  shows a screenshot with the `Snapshot` tag.
* `Snapshots` turns off all live tiles, and `Live` turns them on again.
* A tile is live only while at least 25% of it is on the screen. A tile that
  leaves the screen, or a browser tab that is hidden, disconnects after 30
  seconds.
* The server does not send screenshots of live tiles.

#### The Details Window

The details window of a VM shows a live, view-only console with the banners of
the VM. Click `Take control` to send keys and pointer events to the VM, and
`View only` to stop. `Open console` opens the console page of the VM.

!!! note

    QEMU allows 32 shared clients for each VM. Every console, live tile,
    details window, minimega recording and paste playback counts. Thus a
    browser tab never opens two connections to one VM. While the details window
    or a console pane of a VM is open, the tile of that VM in the same tab
    disconnects and shows its last screen.

#### VM Settings for VNC

phēnix adds two settings to the minimega script of an experiment:

* For each KVM VM, `vm config qemu-override "-vnc unix:" "-vnc
  lossy=on,vnc=unix:"`, before the `overrides` of the topology. With lossy
  updates, the quality level of a console or a tile selects JPEG for regions
  that look like photos. A topology override that matches `-vnc unix:` no
  longer matches, because this override changes that text first.
* For each Linux VM, `vm config vga qxl`, in place of
  `vm config qemu-append -vga qxl`. phēnix does not add it when the `advanced`
  settings of the VM set `vga`. minimega then shows the video card in the
  `vga` column of `vm info`.

!!! note

    The phēnix host must connect to the VNC port of each VM on the compute
    node that runs the VM. minimega selects a random high TCP port for each
    VM. Make sure that the network lets the phēnix host connect to these ports
    on each compute node. When phēnix cannot connect, the console shows
    `phēnix cannot connect to the VNC port of <vm> on <host> (port <n>)`.

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
the VM's details card has its own shark-fin button that does the same thing. It
is possible to start captures on multiple network taps.

To stop a capture, click the name of a green network tap. Stopping the capture
on one interface leaves the VM's other captures running; use
`Stop all packet captures` to stop them all.

A capture cannot be started on a disconnected interface; phēnix says
`Cannot capture traffic on a disconnected interface.` instead of starting one.

phēnix writes the capture file in pcapng format, and the file name ends in
`.pcapng`. The network menu of the VM table can also
start a capture in pcap format (see
[Starting/Stopping Packet Captures](vm-multi-action.md#startingstopping-packet-captures)).
To capture the traffic of all VMs into one file, see
[Experiment Packet Captures](packet-captures.md).

### From the Command Line Binary

To start a packet capture, run the following command, specifying the target
network interface by its name (as declared in the experiment topology, e.g.
`IF0`) or its zero-based index.

```shell
phenix vm capture start <experiment name> <vm name> <iface name/index> </path/to/out file>
```

The `--format` flag sets the capture file format, `pcapng` (the default) or
`pcap`. phēnix sets the extension of the output file name to match the format.
The `ui.captures.max-size` setting limits the size of the file (see
[Settings](settings.md)).

To stop all packet captures on a running VM, use the following command.

```shell
phenix vm capture stop <experiment name> <vm name>
```

To stop the packet capture running on a single interface, without affecting
any other captures running on the same VM, provide the interface name or
index.

```shell
phenix vm capture stop <experiment name> <vm name> [iface name/index]
```

!!! note
    Stopping the capture on a single interface, from the Web-UI or the command
    line, requires minimega 3.3.0 or later.

## Kill a VM

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Kill` in the `Power` section and confirm. The bar of a live
tile and the selection toolbar also ask you to confirm.

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
card, then click `Pause` in the `Power` section and confirm. The bar of a live
tile, the selection toolbar and the top bar of the console page also ask you
to confirm.

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
replicated, before it is deployed again.

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
disabled when the VM is paused or does not have snapshots enabled.

### From the Command Line Binary

To reset the first disk to the initial pre-boot state, run the following command.

```shell
phenix vm reset-disk <experiment name> [vm name] [--label <label>]
```

## Restart a VM

### From the Web-UI

Click on the name of a running VM in a started experiment to open its details
card, then click `Restart` in the `Power` section and confirm. The button is
disabled when the VM is not running. The `Actions` column of the VM table, the
bar of a live tile, the selection toolbar and the console page also ask you to
confirm a restart.

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
card, then click `Shut down` in the `Power` section and confirm. `Shut down` is
also in the VM table's `Actions` column, the selection toolbar, the bar of a
live tile and the top bar of the console page. Each one asks you to confirm.

### From the Command Line Binary

To shutdown a VM, run the following command.

```shell
phenix vm shutdown <experiment name> [vm name] [--label <label>]
```

## Modify VM Settings

### From the Web-UI

There are two ways to modify VM settings:

1. Click on a stopped experiment to access the Stopped Component. You are able to edit the
following:
    * Host
    * CPUs
    * Memory
    * Disk
    * Inject partition
    * Persistence (the `snapshot` flag)
    * Do not boot flag

    Edit a setting in the row of the VM, or click the VM name to open its
    details card. The card has the same settings, and you can also edit the
    labels and annotations of the VM there. A change saves at once, with no
    dialog. The control spins and is disabled until the server answers. If
    the change fails, an error message shows and the control shows the
    previous value. A dot next to a setting tells that its value is
    different from the topology value. `Reset settings` sets all of the
    settings back to the topology values. The card also lists the port
    forwards that the `phenix/startup-autotunnel` annotation opens when the
    experiment starts. To change them, edit the annotation. The
    `Select visible columns` button shows or hides the columns of the table.
1. From a running experiment, click on the VM name and then `Redeploy` in
`More actions` on its details card. You are able to edit the following:
    * CPUs
    * Memory
    * Disk
    * Replicate Original Injection(s)

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
| `-c, --cpu`           | Number of VM CPUs (1-16 is valid).                                                                                |
| `-m, --mem`           | Amount of memory in megabytes (a multiple of 512 from 512 to 131072).                                             |
| `-d, --disk`          | VM backing disk image.                                                                                            |
| `-p, --partition`     | Partition of disk to inject files into.                                                                           |
| `--do-not-boot`       | Set the do-not-boot flag for the VM.                                                                              |
| `--snapshot`          | Set the snapshot (non-persistent) flag for the VM.                                                                |
| `-L, --label-changes key=value` | VM label to set, in `key=value` form. May be repeated to set multiple labels.                                         |
| `--append-labels`      | Append the provided labels to the VM's existing labels instead of replacing them.                                     |
| `-l, --label`         | Apply the change to every VM whose label matches the provided label (supports glob patterns). Use `all` to select every VM in the experiment. May be repeated. |

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
