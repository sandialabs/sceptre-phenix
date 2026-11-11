# Experiment Packet Captures

An experiment capture records the traffic of a running experiment on all of
its hosts. minimega merges this traffic into one capture file on the head node.
phēnix starts, stops, and lists experiment captures. It can also show a running
capture live in WebShark.

To capture the traffic of one VM interface, see
[Packet Capture](vms.md#packet-capture).

## How a Capture Works

Each host adds one Open vSwitch mirror to each bridge that the experiment uses.
The mirror selects the frames that come into the bridge from the experiment's
VM, container, and host taps. It does not select tunnel or trunk ports. Thus the
capture records each frame one time, on the host where the frame came into the
bridge. This is also true across a [GRE mesh](gre-mesh.md) or a VXLAN mesh.

Each host sends its frames to the head node. The head node merges the frames in
timestamp order and writes them to one file in the experiment's files directory.
The file shows in the `Files` tab of the experiment, and it stays there after
the capture stops.

An experiment capture also works on the default `phenix` bridge. Unlike
[netflow](netflow.md), it does not need a bridge of its own.

## Requirements

* A minimega build with experiment captures on every node of the mesh,
  relays included. minimega on the head node refuses a start when a mesh node
  runs a minimega version without experiment captures.
* Synchronized clocks on all hosts. Use chrony or PTP. The head node puts the
  frames of different hosts in order by their timestamps. A clock offset
  changes that order.
* A running experiment.
* The permissions that [Permissions](#permissions) gives.

phēnix does not check the minimega version before it sends a capture command.
It uses the capture commands with all minimega versions. If minimega does not
have experiment captures, the start fails, and phēnix shows the minimega error.
`phenix ui` reads the minimega version of the head node one time, when it
starts, and `phenix doctor` shows that version.

## Capture Options

| Option | Web-UI field | CLI flag | REST field | Default |
|---|---|---|---|---|
| VLANs | `VLANs` | `--vlan` (one for each VLAN) | `vlans` | all VLANs of the experiment |
| BPF filter | `BPF filter (optional)` | `--filter` | `filter` | no filter |
| Snaplen | `Snaplen` | `--snaplen` | `snaplen` | `1600` |
| Format | `Format` | `--format` | `format` | `pcapng` |
| File name | `File name (optional)` | `--name` | `name` | `<exp>[-<vlan>]-<YYYYmmdd-HHMMSS>.pcapng` |

* **VLANs**: Give the VLAN aliases of the experiment, such as `EXP_1`. phēnix
  does not compare the case of the letters. An empty list captures all VLANs.
* **BPF filter**: Each host applies the filter in its kernel. The filter can
  hold letters, digits, spaces, and the characters `. : / [ ] ( ) & | ! < > = + - * ,`.
  Quotes, backslash, and `#` are not permitted. The filter can have a maximum
  of 1024 bytes.
* **Snaplen**: This is the number of bytes of each frame that the capture
  keeps, from 14 to 65535. In the CLI and the REST API, `0` keeps 1600 bytes.
* **Format**: `pcapng` or `pcap` (classic pcap). See [Formats](#formats).
* **File name**: phēnix writes the file to the experiment's files directory.
  It changes each character other than letters, digits, `.`, `_`, and `-` to
  `_`. It sets the extension to `.pcapng` or `.pcap` to match the format. The
  default name has the VLAN alias only when the capture has exactly one VLAN.
  If a file or a running capture already has the default name, phēnix adds
  `-2`, `-3`, and so on. phēnix refuses a name that a running capture writes,
  or that a file in the files directory has.

The `ui.captures.max-size` setting sets the maximum size of each capture file,
in megabytes. The default is `10240` (10 GiB). `0` sets no limit. The setting
also applies to VM interface captures. Restart `phenix ui` after you change
it. See [Settings](settings.md).

### Formats

pcapng records the host and the bridge of each frame. Each host and bridge is a
different interface in the file. The file also records the filter, the snaplen,
and the drop counts of each host. Wireshark, tshark, and WebShark show this
data.

Some tools can read only classic pcap. For these tools, select `pcap`. Or
convert a pcapng file with `editcap -F pcap in.pcapng out.pcap`.

## From the Web-UI

### Start a Capture

1. Open the running experiment.
2. Click the `Capture all experiment traffic` button. It is to the right of
   the netflow button. The `Captures` tab has the same button.
3. In the dialog, set the options. Leave `VLANs` empty to capture all VLANs.
4. Click `Start capture`.

To capture one VLAN, click a network in the `Network` column of the VM table.
Then click `Capture VLAN <alias>…`. The same dialog opens with that VLAN
selected.

The dialog shows the warnings of the [mirror budget](#open-vswitch-mirror-budget)
before you start. If `VLANs` is empty and a host and bridge of the experiment
has 32 mirrors, the dialog disables `Start capture`. If you select VLANs, the
dialog shows a warning instead. phēnix refuses that start only if those VLANs
use that bridge. The note `Frames from hardware behind a trunk are not captured.`
tells you about a limit of the capture.

After the start, a message shows the capture ID and the budget warnings.
phēnix asks for no confirmation to start or to stop a capture.

A user who does not have `experiments/captures` `create` does not see the
button or the menu item.

### Captures Tab

The `Captures` tab lists the experiment captures. The tab label shows the
number of running captures. The tab reads the list again every 5 seconds while
it shows. It also reads the list when a capture starts or stops. While a
capture runs and another tab shows, the page reads the list every 30 seconds.

| Column | Value |
|---|---|
| `ID` | The ID that minimega gives the capture |
| `Status` | The state of the capture |
| `Scope` | The VLANs and the filter, or the error of a failed capture |
| `Started` | The start time |
| `Size` | The size of the file |
| `Packets` | The packets in the file |
| `Drops` | The packets that the hosts dropped |
| `Actions` | The actions for the capture |

minimega gives these states:

* `starting`, `running`, `stalled`, and `stopping`: minimega writes the file.
  A capture is `stalled` when a host sends no data for 10 seconds.
* `stopped`: The file is complete.
* `full`: The file reached its maximum size.
* `failed`: The capture stopped because of an error. The `Scope` column shows
  the error.
* `orphaned`: A host lost its connection to the head node and stopped its part
  of the capture.

Click the expand button of a capture to see a row for each host and bridge:

| Column | Value |
|---|---|
| `Host`, `Bridge` | The host and the bridge |
| `Ports` | The VM ports that the host captures |
| `Packets` | The packets that the head node wrote to the file |
| `Drops` | The packets that the kernel, the capture library, or a full queue dropped |
| `Lost` | The packets lost between the host and the head node |
| `Late` | The packets that came after the reorder window. The file has them out of order. |
| `Lag` | How far the merge on the head node is behind |
| `Skew` | How far the clock of the host is from the clock of the head node |

A skew of more than 500 ms shows a warning. 500 ms is half of the default
reorder window of minimega. With a large skew, many frames are late.

The actions of a capture are:

* Running: `Open in WebShark` and `Stop`.
* Stopped: `Open in WebShark`, `Download`, and `Delete`. `Delete` removes the
  file and asks you to confirm.
* Failed: `Delete`.

`Download` and `Delete` use the experiment files. They need
`experiments/files` `get` and `delete`.

### Live View in WebShark

Click `Open in WebShark` to open the capture on the WebShark page. WebShark
must be installed. The page lists the experiment captures in the
`Experiment captures` group, and a running capture has the label `(live)`.
The view of a running capture follows the file as it grows. Its title is, for
example, `exp1 capture 3 (live)`.

Each frame has its 802.1Q VLAN tag, because the mirror sends the frames to a
trunk port.

For a pcapng capture, the page shows a display filter for each host, for
example `frame.interface_name == "compute1/mega_bridge_exp1"`. Click the copy
button of a filter. Then paste the filter into the WebShark display filter box.

## From the Command Line Binary

To start an experiment capture of all VLANs, use the following command.

```shell
phenix experiment capture start <experiment name>
```

To capture some VLANs, give `--vlan` one time for each VLAN. The other options
have the flags that [Capture Options](#capture-options) gives.

```shell
phenix experiment capture start exp1 --vlan EXP_1 --vlan EXP_2 --filter "tcp port 502" --format pcap
```

The command prints the capture ID and the file. It prints each budget warning
as a `Warning:` line.

To list the experiment captures and the mirror budget, use the following
command. The list has a row for each capture, and a row for each host and
bridge under it.

```shell
phenix experiment capture list <experiment name>
```

To stop an experiment capture, use the following command. It prints the last
counters of the capture. The file stays in the experiment's files directory.

```shell
phenix experiment capture stop <experiment name> <capture ID>
```

The CLI applies the `ui.features` and `ui.captures.max-size` settings, as
`phenix ui` does. See [phenix experiment](cli/experiment.md#phenix-experiment-capture)
for all flags.

## From the REST API

| Method | Path | Result |
|---|---|---|
| `GET` | `/api/v1/experiments/{exp}/captures` | The VM interface captures, the experiment captures (`experimentCaptures`), the support status (`support`), and the mirror budget (`budget`) |
| `POST` | `/api/v1/experiments/{exp}/captures` | Starts a capture. The body has the fields that [Capture Options](#capture-options) gives. |
| `GET` | `/api/v1/experiments/{exp}/captures/{id}` | One capture, with a row for each host and bridge |
| `DELETE` | `/api/v1/experiments/{exp}/captures/{id}` | Stops a capture and gives its last counters |
| `GET` | `/api/v1/experiments/{exp}/captures/{id}/stream` | Streams a running capture. Add `?from=now` to skip the frames before the request. |
| `POST` | `/api/v1/experiments/{exp}/webshark` | Opens WebShark on a capture, with the body `{"capture": "<id>"}` |

A start answers `201` with the capture and its warnings. It answers `409` when
the experiment does not run, when the mirror budget refuses the start, or when
a running capture or a file has the file name. It answers `501` when minimega
does not support experiment captures. The `/ws` websocket sends an
`experiment/capture` resource named `<exp>/<id>` with the action `start` or
`stop`.

The stream sends the capture as a websocket or as a chunked body. A pcapng
stream starts with the section header and the interface blocks, also with
`from=now`. For example, show a running capture in Wireshark:

```shell
curl -N -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  "http://localhost:3000/api/v1/experiments/exp1/captures/3/stream" | wireshark -k -i -
```

See [API](api.md) for the full route reference.

## Open vSwitch Mirror Budget

Open vSwitch applies a maximum of 32 mirrors on one bridge. It accepts a 33rd
mirror without an error, but it ignores it. Each running experiment capture
uses one mirror on each host and bridge where the experiment's VMs have an
interface on a captured VLAN. Tap mirrors, bridge captures, other experiments
on a shared bridge, and mirrors made outside minimega use the same 32 mirrors.

phēnix counts the mirrors of each host and bridge that the experiment uses.
It gives each host and bridge a level:

| Level | Condition | What phēnix does |
|---|---|---|
| `ok` | None of the conditions below | Nothing |
| `half` | The experiment uses more than 16 mirrors | The `Captures` tab shows a warning |
| `near` | The bridge has 28 or more mirrors | The `Captures` tab shows a warning |
| `full` | The bridge has 32 mirrors | The `Captures` tab shows an error. For all VLANs, the dialog disables `Start capture` |

phēnix refuses a start, and sends nothing to minimega, in these conditions:

* A host and bridge that the capture needs has 32 mirrors. The message is, for
  example, `compute1 / mega_bridge_exp1 has 32 of 32 Open vSwitch mirrors`.
* The experiment already runs 32 experiment captures.

After a start, phēnix warns when the capture makes a host and bridge pass a
limit:

* `exp1 uses 18 of 32 mirrors on compute1 / mega_bridge_exp1`: The experiment
  uses more than 16 mirrors.
* `compute1 / mega_bridge_exp1 has 29 of 32 mirrors; 3 are left (18 by exp1, 11 by others)`:
  The bridge has 28 or more mirrors.

If phēnix cannot count the mirrors, it starts the capture and gives the
warning `cannot count Open vSwitch mirrors: <error>`.

## Permissions

The RBAC resource is `experiments/captures`. See
[User Administration](user-administration.md#resource-experimentscaptures).

| Verb | Permits |
|---|---|
| `list` | The `Captures` tab and the capture list |
| `create` | A start |
| `get` | One capture. With `experiments/files` `get`, also the stream and WebShark. |
| `delete` | A stop |

Of the built-in roles, only `global-admin` has `create` and `delete`. One file
holds all of the experiment's traffic. Give these verbs only to users who can
read all of that traffic. phēnix writes an audit event for each start, stop,
and stream. See [Logging](logging.md).

## Limits

* The capture does not record frames from hardware behind a trunk, or behind
  a bond to a physical NIC.
* A packet that a router VM routes shows one time for each VLAN that it
  crosses.
* A frame can be larger than the MTU, because of generic segmentation offload
  (GSO). Other minimega captures do the same.
* In pcapng, minimega finds the VM port of a frame from the source MAC address.
  A VM that bridges frames, or that sends with another MAC address, gets the
  wrong port or no port.
* An experiment can run a maximum of 32 experiment captures at the same time.
* The head node receives all captured traffic. If hosts drop frames, the
  `Drops` and `Lost` columns show the numbers. Use the VLANs, the filter, and
  the snaplen to capture less traffic.
* When the experiment stops, minimega stops all of its captures. The files
  stay.
* When phēnix restarts, the live WebShark views stop. The captures continue in
  minimega, and phēnix finds them again.

## Troubleshooting

### The Capture Controls Are Disabled

The controls show the reason. The `Captures` tab shows it too.

| Reason | Fix |
|---|---|
| `the server did not report capture support` | Reload the page. If the controls stay disabled, make sure that `phenix ui` runs. |

`phenix doctor` shows the capture support in its `minimega-captures` row.

phēnix accepts `minimega-captures` in the `ui.features` setting, but the
feature has no effect: phēnix uses the capture commands with all minimega
versions.

### The Start Is Refused

* `<host> / <bridge> has 32 of 32 Open vSwitch mirrors`: Stop an experiment
  capture or a tap mirror on that host. `phenix experiment capture list` shows
  the mirror count of each host and bridge.
* `already runs 32 experiment captures`: Stop an experiment capture of the
  experiment.
* `the capture file is in use`: A running capture writes the file, or the
  file exists. Give another file name.
* `experiment is not running`: Start the experiment first.
* An error from minimega about the version of a mesh node: Upgrade minimega on
  that node.

### A Skew Warning Shows

The clock of a host is more than 500 ms from the clock of the head node. The
file has frames of that host out of order. Synchronize the clocks of all hosts
with chrony or PTP.
