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
or per-experiment via the UI by expanding the `Advanced Options` section when
creating a new experiment and providing the `Default Bridge Name` setting.

![Expanded Advanced Options with Deployment Mode, Default Bridge Name, VLAN Range, Git Workflow Branch Name, and annotations for every VM and for the experiment.](images/create_exp_options.png){: width=400 .center}

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

Running experiments will have a green network icon to the right of the VM search
box that, when clicked, will start netflow capture for the non-default bridge
the experiment is configured to use for all VM network interfaces.

![Running experiment toolbar with the VMs, Files, VNC and Netflow tabs, the Hosts, Labels and Paginate controls, a Find a VM box, and the stop, netflow and column picker buttons.](images/exp_netflow_button.png){: width=800 .center}

Once enabled, the icon will turn red (to signify it can be stopped), and the
`Netflow` tab, the last tab of the running experiment, will be updated with a
table of netflow data from minimega as it streams in. Until the first line
arrives, the tab shows `Waiting for netflow lines from the running capture.`

![Netflow tab reading 22 lines, newest first, with a Download captures button and a table of flows with time, source, destination, protocol, packet and byte counts.](images/netflow_table.png){: width=800 .center}

To stop netflow capture for the experiment, click the red network icon.
