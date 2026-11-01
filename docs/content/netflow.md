# Experiment Netflow

The minimega platform supports [netflow
capture](https://sandia-minimega.github.io/minimega/reference/minimega/#capture), wherein flows
through an Open vSwitch bridge can be parsed into an ASCII format and forwarded
to a remote endpoint for additional consumption.

phēnix extends this capability by making the minimega netflow ASCII format
available for streaming over WebSockets for experiments. The only requirement
for enabling netflow for an experiment is that the experiment be configured to
use a bridge name that is not the default `phenix` bridge. This can be done
automatically for all experiments by enabling [auto bridge mode](bridge-mode.md),
or per-experiment via the UI by expanding the `Options` section when creating a
new experiment and providing the `Default Bridge Name` setting.

![screenshot](images/create_exp_options.png){: width=250 .center}

!!! note
    The `Default Bridge Name` field is only shown when the server is running in
    `manual` bridge mode. In `auto` mode the bridge name is always the
    experiment name, so the field is hidden.

This can also be done via the CLI by providing the `--default-bridge, -b` option
when using the `phenix exp create` subcommand.

!!! warning
    As mentioned above, the experiment netflow capability **will not work** if a
    default bridge name is not explicitly specified for an experiment. This is
    to prevent network flows across multiple experiments from being captured.

## Usage

Running experiments will have a green network icon to the left of the VM search
box that, when clicked, will start netflow capture for the non-default bridge
the experiment is configured to use for all VM network interfaces.

![screenshot](images/exp_netflow_button.png){: width=800 .center}

Once enabled, the icon will turn red (to signify it can be stopped), and a new
`Netflow` tab will appear next to the `VMs` and `Files` tabs for the running
experiment. This tab will be updated with ASCII netflow data from minimega as it
streams in.

![screenshot](images/exp_netflow_tab.png){: width=800 .center}

To stop netflow capture for the experiment, click the red network icon.

## Recorded Captures

phēnix records every netflow capture as it streams it, to the experiment's
files as `netflow/<id>.txt`, where `<id>` is the capture's UTC start time, such
as `20261028T153012.345Z`. The file holds every line minimega sent, exactly as
it arrived, one flow per line:

```text
2026-10-28T09:30:12-06:00	1.5s	6	10.0.0.1:1234	<->	10.0.0.2:443	2	2048
```

The fields are the flow's start time, its duration, the IP protocol number, the
source and destination address and port, and its packet and byte counts. The
file also appears in the experiment's `Files` tab, where it can be downloaded
like any other experiment file.

`GET /api/v1/experiments/{name}/netflow/captures` lists the experiment's
recorded captures, newest first, with when each started and stopped, its flow
count and its size. Each capture downloads in three formats:

| Format | File | Contents |
| :--- | :--- | :--- |
| Text | `<exp>-netflow-<id>.txt` | The recorded lines, unchanged. The download can be resumed. |
| CSV | `<exp>-netflow-<id>.csv` | One row per flow, under a header row of the field names below. |
| JSONL | `<exp>-netflow-<id>.jsonl` | One JSON object per flow per line, with the field names below as keys. |

CSV and JSONL name the fields as the
[Elastic Common Schema](https://www.elastic.co/guide/en/ecs/current/index.html)
does, so the files load into Elasticsearch, a spreadsheet or a script as they
are:

| Field | Value |
| :--- | :--- |
| `event.start` | When the flow started, in UTC (RFC 3339) |
| `event.end` | When the flow ended, in UTC (RFC 3339) |
| `event.duration` | The flow's duration in nanoseconds |
| `source.ip`, `source.port` | The source address and port |
| `destination.ip`, `destination.port` | The destination address and port |
| `network.transport` | The protocol's IANA name, such as `tcp`, `udp` or `icmp`; empty in CSV, and left out of JSONL, for a protocol without one |
| `network.iana_number` | The IP protocol number |
| `network.packets`, `network.bytes` | The flow's packet and byte counts |

A line that does not parse as a flow is kept in the text download but left out
of CSV and JSONL. The audit event of the download counts the lines left out
(see [Logging](logging.md)).

A capture still running is listed with the state `recording`, and downloads as
it was when the download started. A capture the phēnix server did not finish,
because the server stopped or restarted while it was recording, is listed as
`interrupted`, with no flow count; its recorded flows still download.

Each capture records at most `ui.netflow.max-size` megabytes, 100 by default
(see [Settings](settings.md)). A capture that reaches the limit, or hits a
write error, stops recording and is marked truncated, and goes on streaming to
the `Netflow` tab. `0` turns recording off. If a capture cannot be recorded,
for example because the disk is full, it still starts and streams, and the
server logs the error.

!!! note
    phēnix records every netflow datagram it reads, but on a busy bridge or a
    slow disk the host can drop datagrams before phēnix reads them, without
    counting them. phēnix asks for a 4 MiB receive buffer, which Linux limits
    to `net.core.rmem_max`; raise that on the host (for example
    `sysctl -w net.core.rmem_max=4194304`) for the larger buffer to take
    effect.

!!! warning
    Captures are not authenticated. phēnix records every datagram that reaches
    the capture's UDP port, whoever sent it, so a host that can reach the
    phēnix server can add lines to a capture, or fill it to its size limit.
    Limit who can reach the server's UDP ports.

Recorded captures stay after the experiment stops, and are never deleted
automatically. When an experiment is deleted, its captures stay in
`<base-dir.phenix>/images/<exp>/files/netflow/` with no route to them, as its
packet captures do; an experiment created later with the same name lists them
again. Remove that directory on the host to clean up.

Downloading a capture needs the `experiments/netflow` `get` permission, the same
as the `Netflow` tab. The text file is also reachable through the Files tab,
with the `experiments/files` `get` permission. There is no CLI command for
recorded captures; use the API routes `GET /api/v1/experiments/{name}/netflow/captures`
and `GET /api/v1/experiments/{name}/netflow/captures/{id}?format=text|csv|jsonl`
(see the [API](api.md)).
