# Experiments

For experiments spanning multiple minimega hosts, see [GRE Mesh](gre-mesh.md)
to trunk experiment networks between hosts with `--use-gre-mesh`, including
the physical network MTU requirements.

## Listing Experiments

### From the Web-UI

Click on the `Experiments` tab. This will display all available experiments that
the user has access to view or edit.

### From the Command Line Binary

This will display a list of all available experiments: it is run as a `root`
user.

```bash
phenix exp list
```

<br>

## Starting / Stopping Experiments

### From the Web-UI

Clicking the `stopped` button will start the experiment; similarly the `started`
button will stop the experiment. A progress bar is used to update the progress
of starting an experiment. During the update to the experiment -- starting or
stopping -- it will not be accessible or available to delete.

### From the Command Line Binary

```bash
phenix exp start <experiment name>
```

Or ...

```bash
phenix exp stop <experiment name>
```

Or ...

```bash
phenix exp restart <experiment name>
```

Optionally, you can use the `--dry-run` flag to do everything except call out to
minimega.

After an experiment stops successfully, phēnix deletes the VM disk snapshots
created for file injection. To keep these snapshots for debugging, use
`--keep-injection-snapshots` or its `-K` shorthand:

```bash
phenix exp stop --keep-injection-snapshots <experiment name>
```

Only `phenix exp stop` accepts this option. Stopping an experiment from the
Web-UI, or with `phenix exp restart`, always deletes the snapshots.

If minimega cannot kill an experiment's VMs, for example because it is not
answering, the stop fails and the experiment stays running, so you can stop it
again once minimega answers. To mark it stopped anyway, use `--force` or its
`-f` shorthand, then clear any of its VMs still in minimega by hand:

```bash
phenix exp stop --force <experiment name>
```

The API offers the same as
`POST /api/v1/experiments/{name}/stop?force=true`. Until then, the experiment's
periodic apps keep running and its delayed VMs keep waiting to start.

The `phenix exp --help` command will output:

```text
Experiment management

Usage:
  phenix experiment [flags]
  phenix experiment [command]

Aliases:
  experiment, exp

Available Commands:
  apps            List of available apps to assign an experiment
  create          Create an experiment
  delete          Delete an experiment
  edit            Edit an experiment
  list            Display a table of available experiments
  reconfigure     Reconfigure an experiment
  restart         Restart an experiment
  schedule        Schedule an experiment
  schedulers      List of available schedulers to assign an experiment
  scorch          Start a Scorch run for experiment
  start           Start an experiment
  stop            Stop an experiment
  trigger         Trigger lifecycle stage for app(s) in experiment

Flags:
  -h, --help   help for experiment

Use "phenix experiment [command] --help" for more information about a command.
```

!!! note
    For a list of global flags supported across all `phenix` subcommands, see [Global Flags](settings.md#global-flags).

### Disk Check Before a Start

Before minimega launches a VM, phēnix checks the disk images and CD-ROM images
of the VMs that boot. Every start does this check: the Web-UI, the CLI, the
REST API, `phenix workflow apply`, and the bulk `Start` and `Restart` of VMs.
phēnix examines these files:

* Each `hardware.drives[].image` of a VM that is not external and does not
  have `do_not_boot: true`. phēnix follows the backing chain of a qcow2 image
  to its base. It reads only the image headers, and does not run `qemu-img`.
* The CD-ROM image of the VM (`advanced.cdrom`), and each drive image with the
  `.iso` extension. phēnix checks only that the file exists.

phēnix refuses the start when a file is missing, when a backing chain loops,
or when a chain has more than 64 images. It also refuses the start when qemu
cannot open an image of the chain. Examples are an encrypted image and an
unknown qcow2 version. See
[Image Format Versions](disks.md#image-format-versions). The experiment stays
stopped, and phēnix sends no command to minimega. Pre-start apps run before
the check. The default `startup` app marks a VM `do_not_boot` when it cannot
find the first disk image of the VM. phēnix still checks that VM. The error
names each VM, each disk, and the missing file:

```text
Error: unable to start the foo experiment: qemu cannot open 2 disks on 1 VM: 2 errors occurred:
    * VM host-01: disk kali.qc2: backing file /phenix/images/ro_kali.qc2 is missing
    * VM host-01: CD-ROM tools.iso: CD-ROM image /phenix/images/tools.iso is missing
```

The error lists at most 20 disks, then `and N more`. The Web-UI shows the
count, then each VM with a list of its disks and problems. It lists 10 disks,
and puts the others under `Show N more disks`, which opens with a click or the
keyboard. After a start of more than one experiment, or a bulk `Start` or
`Restart` of VMs, the summary shows each item that failed. Its problems are
under `Show N problems`, which is open when only one item failed. The REST API
answers `400` with the code
`broken_disks`. Its `details.disks` holds one object for each disk, with the
fields `vm`, `drive` (`disk` or `cdrom`), `image`, `path`, `missing`, and
`problem`. The error shows full paths to every user who can start the
experiment.

These problems do not refuse the start. phēnix shows each one as a warning:

* A vmdk or qed image, or a backing format other than qcow2 or raw. phēnix
  does not examine the chain past that image.
* A qcow2 image with an external data file, or a backing file with a protocol
  name, such as `nbd://` or `json:`.
* A qcow version 1 image, which some qemu builds cannot open, or a qcow2 image
  that qemu did not close cleanly. The warning gives the fix.
* A qcow2 image that qemu marked as corrupt, when qemu only reads it: a
  backing file, or a disk of a VM with `snapshot: true`.
* A file that phēnix cannot read, for example because of its permissions.
* A missing file that a cluster node other than the head node can have (see
  [Clusters](#clusters)).
* A check that does not complete in 20 seconds. The start continues, and the
  warning names each image that phēnix did not check.

The CLI shows the warnings. A start from the Web-UI or the REST API writes the
warnings only to the server log.

To change the check, use the `experiment.disk-check` setting (see
[Settings Reference](settings.md#settings-reference)):

| Value | Result |
| :--- | :--- |
| `error` (default) | A problem refuses the start. |
| `warn` | The experiment starts, and each problem is a warning. |
| `off` | phēnix does not check the images. |

With `phenix exp start --dry-run` or `--treat-mm-errors-as-warnings`, each
problem is a warning.

#### Clusters

phēnix examines only the files on the head node. A missing file has this
result:

| Case | Result |
| :--- | :--- |
| phēnix cannot see the minimega files directory, for example because phēnix and minimega run in different containers, or minimega did not give its directory | Warning |
| The VM boots from a snapshot (`snapshot: true`, the default). minimega makes the snapshot on the head node. | Error |
| The image is in the minimega files directory. minimega copies the image and its backing files from the head node to the other nodes. | Error |
| The image is outside the files directory, and the VM runs on the head node: `deploy-mode: only-headnode`, a schedule to the head node, or a cluster with only the head node | Error |
| The image is outside the files directory, and the VM can run on a different node | Warning |
| phēnix cannot get the cluster hosts from minimega | Warning |

phēnix does not examine the files on the other nodes. Thus a file that is in
the files directory of a compute node, but not on the head node, refuses the
start. Copy the file to the head node, or set `experiment.disk-check` to
`warn`. phēnix also keeps the list of cluster hosts for a short time, so it can
miss a node that joined the cluster recently.

#### Restarts

`phenix exp restart`, and a restart from `phenix workflow apply`, check the
disks before they stop the experiment. If the start would fail, phēnix refuses
the restart, and the experiment continues to run.

An app can change the disks of a VM before the start. Thus, when the scenario
has a user app (an app that is not a default app), the check before the stop
only shows warnings. The check before the start can still refuse the start.
Then the experiment stays stopped.

#### Bulk VM Actions

The bulk `Start` and `Restart` of VMs (`vms.action` jobs) do the same check for
the selected VMs that your role can start. phēnix checks only a VM that
minimega launches again: a VM in the `QUIT` or `ERROR` state, or a VM whose
state phēnix cannot get. A VM that is paused or running already has its images
open, so phēnix does not check it. A VM with a broken image fails at once, and
the job starts the other VMs. A start or restart of one VM from its own route
does not do this check.

## Create a New Experiment

### From the Web-UI

![screenshot](images/create_exp.png){: width=800 .center}

Click the `+` button to the right of the filter field.

![screenshot](images/create_exp_dia.png){: width=400 .center}

Enter `Experiment Name` and `Experiment Topology`, the remaining selection are
optional. In this example, `bennu` is an example topology and is not included
by default. You will need to [create](configuration.md) your own topology(ies).

#### Purely via the Web-UI (Uploading Topo/Scenario Files)

You can create an experiment entirely through the Web-UI using custom topology and scenario files without needing to use the CLI:

1. **Upload Configurations in the Configs Tab**:
    * Navigate to the **Configs** tab.
    * Click the **Upload** button to upload your topology file (`.yaml`).
    * (Optional) Click **Upload** again to upload your scenario file (`.yaml`).
    * Verify that your uploaded configurations appear in the table (e.g., `topology/my-topology` and `scenario/my-scenario`).

2. **Create Experiment in Experiments Tab**:
    * Navigate to the **Experiments** tab.
    * Click the `+` button to open the creation dialog.
    * Enter an **Experiment Name**.
    * Select your uploaded topology from the **Experiment Topology** dropdown.
    * (Optional) Select your uploaded scenario from the **Experiment Scenario** dropdown.
    * Click **Save** to create the experiment.

### From the Command Line Binary

You can create an experiment using either saved configuration names from the phēnix store or direct file paths to topology and scenario configuration files (`.yaml`).

```bash
# Using stored configuration names
phenix experiment create <experiment name> -t <topology name>
phenix experiment create <experiment name> -t <topology name> -s <scenario name>

# Using direct file paths for topology and scenario
phenix experiment create <experiment name> -t /path/to/topology.yaml
phenix experiment create <experiment name> -t /path/to/topology.yaml -s /path/to/scenario.yaml

# Specifying a base directory and disabled apps
phenix experiment create <experiment name> -t /path/to/topology.yaml -s /path/to/scenario.yaml -d </path/to/dir/> --disabled-apps "app1,app2"
```

The `phenix exp create --help` command will output:

```text
Create an experiment

  Used to create an experiment from existing configurations; can be a
  topology, or topology and scenario, or paths to topology/scenario
  configuration files (YAML or JSON). (Optional are the arguments for
  scenario or base directory.)

Usage:
  phenix experiment create <experiment name> [flags]

Examples:

  phenix experiment create <experiment name> -t <topology name or /path/to/filename>
  phenix experiment create <experiment name> -t <topology name or /path/to/filename> -s <scenario name or /path/to/filename>
  phenix experiment create <experiment name> -t <topology name or /path/to/filename> -s <scenario name or /path/to/filename> -d </path/to/dir/>
  phenix experiment create <experiment name> -t <topology name or /path/to/filename> -s <scenario name or /path/to/filename> --disabled-apps "app1,app2"

Flags:
  -d, --base-dir string         Base directory to use for experiment (optional)
  -b, --default-bridge string   Default bridge name to use for experiment (optional) (default "phenix")
      --disabled-apps strings   Comma separated ist of apps to disable
  -h, --help                    help for create
  -s, --scenario string         Name of an existing scenario to use (optional)
  -t, --topology string         Name of an existing topology to use
      --vlan-max int            VLAN pool maximum
      --vlan-min int            VLAN pool minimum
```

## Deleting Experiments

### From the Web-UI

The experiment must be stopped before it can be deleted; click the trash can
icon next to the experiment to delete it.

### From the Command Line Binary

An experiment must be stopped before it can be deleted:

```bash
phenix exp stop <experiment name>
phenix exp delete <experiment name>
```

Alternatively, the `-f`/`--force` flag can be used to automatically stop a
running experiment before deleting it, similar to `docker rm -f`, even when
minimega cannot kill its VMs, which are then left for you to clear by hand:

```bash
phenix exp delete -f <experiment name>
```

Using `all` instead of a specific experiment name will delete all stopped
experiments (or, when combined with `--force`, all experiments regardless of
whether they are running).

The `phenix exp delete --help` command will output:

```text
Delete an experiment

  Used to delete an existing experiment; experiment must be stopped.
  Using 'all' instead of a specific experiment name will include all
  stopped experiments. Use the -f/--force flag to automatically stop
  a running experiment before deleting it.

Usage:
  phenix experiment delete <experiment name> [flags]

Aliases:
  delete, del

Flags:
  -f, --force   Stop a running experiment before deleting it
  -h, --help    help for delete
```

## Scheduling an Experiment

### From Web-UI

The experiment must be stopped; click on the experiment name to enter the
Stopped Experiment component. Click on the hamburger menu to the right of the
filter field and start button to select a desired schedule.

![screenshot](images/schedule.png){: width=400 .center}

### From the Command Line Binary

The list of available schedules can be found by running the following command.

```bash
phenix experiment schedulers
```

Then apply the desired schedule with the following command.

```bash
phenix experiment schedule <experiment name> <algorithm>
```

## Triggering App Lifecycle Stages

You can manually trigger an app lifecycle stage for an experiment without
stopping and restarting it.

### From the Command Line Binary

The `phenix exp trigger` command runs one app lifecycle stage on demand:

```bash
phenix exp trigger <lifecycle> <experiment name> [<app name> ...]
```

Where `<lifecycle>` is one of the following stages, or its shorthand:

* `configure` (or `config`)
* `pre-start` (or `pre`)
* `post-start` (or `post`)
* `running` (or `run`)
* `cleanup` (or `clean`)

Providing no app names triggers the stage for every app in the experiment. Use
`all` instead of an experiment name to trigger the stage in every experiment.
The `running` stage is skipped for experiments that are not running.

**Examples:**

Trigger the running stage for all apps in an experiment:

```bash
phenix exp trigger running my-experiment
```

Trigger the running stage for a specific app:

```bash
phenix exp trigger running my-experiment soh
```

Trigger the configure stage for all experiments:

```bash
phenix exp trigger configure all
```

### `trigger configure` vs. `reconfigure`

`phenix exp trigger configure` and `phenix experiment reconfigure` both re-run
the `configure` lifecycle stage, but they serve different purposes and are not
interchangeable:

| | `phenix exp trigger configure` | `phenix experiment reconfigure` |
|---|---|---|
| Use case | Manually re-run a specific app's (or apps') configure hook on demand, e.g. for debugging | Re-apply configuration after editing an experiment's stored topology, scenario, or deployment settings |
| Targets specific apps | Yes, via `[<app name> ...]` | No, always applies to every app |
| Validates the experiment config | No | Yes |
| Runs registered config hooks | No | Yes |
| Resets the minimega bridge | No | Yes (deletes it so it's recreated with current settings, unless using the GRE mesh) |
| Guards against running experiments | No | Yes (refuses to run if the experiment is running) |

Use `phenix experiment reconfigure` after changing an experiment's stored
settings. Use `phenix exp trigger configure` for targeted, on-demand
re-invocation of one or more apps' configure hooks, such as when debugging an
app.

### Deprecated Command

The `phenix exp trigger-running` command, and its `trig` alias, are deprecated
and will be removed in a future release. Use `phenix exp trigger running`
instead. The deprecated command:

```bash
phenix exp trigger-running my-experiment [<app name> ...]
```

Is equivalent to:

```bash
phenix exp trigger running my-experiment [<app name> ...]
```

## VLAN Aliases

Topologies refer to VLANs by alias, such as `MGMT` or `EXP-1`,
which is mapped by phēnix to a numerical VLAN ID.

### From the Command Line Binary

The `phenix vlan alias` command can list and change VLAN aliases.

```bash
# List VLAN aliases of every experiment
phenix vlan alias

# List VLAN aliases of a single experiment
phenix vlan alias <experiment name>

# Print the VLAN ID of a specific alias for an experiment
phenix vlan alias <experiment name> <alias name>

# Set the alias to a VLAN ID (add -f to overwrite an existing alias)
phenix vlan alias <experiment name> <alias name> <vlan id>
```

For example, if the `MYVLAN` alias of `my-experiment` maps to VLAN 105:

```bash
$ phenix vlan alias my-experiment MYVLAN
105
```

A running experiment reports the VLAN IDs minimega assigned when it started. A
stopped experiment has no ID for an alias unless one was set explicitly, so
most alias lookups will fail until the experiment starts.

## Common Workflows

### 1. Basic Experiment Lifecycle

```bash
# 1. Create experiment from topology/scenario files
phenix exp create my-experiment -t /path/to/topology.yaml -s /path/to/scenario.yaml

# 2. Start the experiment
phenix exp start my-experiment

# 3. View status and inspect VMs
phenix exp list
phenix vm info my-experiment

# 4. Stop the experiment when testing finishes
phenix exp stop my-experiment

# 5. Delete the experiment
phenix exp delete my-experiment
```

### 2. Updating and Reconfiguring an Experiment

```bash
# Reconfigure a stopped experiment after modifying underlying configuration files
phenix exp reconfigure my-experiment
```

### 3. Dry-Run Deployment Testing

```bash
# Test experiment startup logic without deploying minimega VMs
phenix exp start my-experiment --dry-run
```
