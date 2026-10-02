# Import and Export

Builder v2 can start a draft from a phenix config or from a Builder v2
file, and it can save a diagram as a file in six formats. None of this
changes a config on the phenix server: in the web UI, only **Publish**
writes configs (see [Publishing](publishing.md)). On the command line,
`phenix builder publish` makes a topology from a Builder file.

| To | Use | Where | Result |
|---|---|---|---|
| Start from a Topology or Experiment config | **Import** | Drafts page | A new draft |
| Open a Builder document, or a published diagram | **Upload** | Drafts page and editor toolbar | A new draft |
| Save the diagram as a file | **Export** | Editor toolbar | A file |
| Make a topology from a Builder file | `phenix builder publish` | Command line | A Topology config (see [From the command line](#from-the-command-line)) |
| Show the diagram of a topology kept in files | A `builder-doc` annotation with a `path` | The Topology config | A card under **Published Diagrams** (see [A Builder file beside a topology](#a-builder-file-beside-a-topology)) |

The examples on this page use the [example lab](index.md#the-example-lab).

## Importing a topology or experiment

**Import** makes a draft from a Topology or Experiment config. The config can
be one stored in phenix, or a file you upload. The phenix server converts
the config, so the draft holds what phenix would read from it.

### From a stored config

To import the riverside-water topology:

1. On the drafts page, select **Import**. The **Import topology or
   experiment** dialog opens.
2. Keep **Source** set to **Stored config**, and **Source kind** set to
   **Topology**.
3. In **Source name**, choose `riverside-water`.
4. Select **Import**. The dialog says "This import has 1 warning." and lists
   it: "Added 2 nodes from included topology corp-services (2 nodes). They
   are shown read only: edit them in their own topology. Publishing keeps
   includeTopologies instead of copying them."
5. Select **Continue to editor**.

The dialog in step 3:

![The Import topology or experiment dialog with Stored config, Source kind Topology and Source name riverside-water selected, and the Cancel and Import buttons.](../images/builder-v2/quickstart-import-dialog.png)

The editor opens a new draft named riverside-water. The header counts 12
devices, 4 switches, 4 networks and 15 connections. The layout menu says
**Default**: the devices are in rows on a grid, with the switches below
them. Choose a layout to arrange them (see [Layouts](diagrams.md#layouts)).

To stop at step 5, select **Cancel**: the dialog closes and no draft is
made. An import without warnings opens the editor at once, without step 5.

### Importing an experiment

An experiment imports the same way. To import the riverside experiment:

1. Select **Import**.
2. Keep **Stored config**. Set **Source kind** to **Experiment**, and choose
   `riverside` in **Source name**.
3. Select **Import**. The dialog says "This import has 2 warnings."
4. Select **Continue to editor**. The new draft is named riverside.

The warnings in step 3:

![The import of experiment riverside with two warnings: the two nodes from the included topology corp-services are shown read only, and the experiment fields baseDir, defaultBridge and deployMode are not kept; with Cancel and Continue to editor.](../images/builder-v2/import-experiment-warnings.png)

An imported experiment differs from an imported topology in these ways:

- The draft keeps the experiment's scenario. When your role can list the
  stored Scenario that the experiment names, the draft refers to it: the
  Inspector says "Stored scenario riverside-water". Otherwise the draft
  keeps the experiment's own copy of the scenario, as an uploaded scenario,
  and the import warns: "scenario "riverside-water" is not available on this
  server, so the experiment's copy of it is attached as an uploaded
  scenario".
- The experiment's VLAN aliases become the **VLAN alias** of each network.
- Some experiment fields have no place in a diagram. The import names them,
  for example "experiment fields not represented in the builder document:
  baseDir, defaultBridge, deployMode". The experiment's VLAN range is not
  kept either.
- The files that the experiment's apps injected into VMs when it started
  are left out, without a warning. The topology's own injections are kept.

### Importing an uploaded config

An uploaded config does not need to be stored in phenix first. To import
the pump station of the example lab from
[pump-station.topology.yaml](examples/pump-station.topology.yaml):

1. Select **Import**.
2. Set **Source** to **Uploaded config**.
3. In **Topology or Experiment config**, choose `pump-station.topology.yaml`.
   The file can be JSON or YAML, up to 5 MiB.
4. Select **Import**.

This config has no warnings, so the editor opens at once. The new draft is
named pump-station, after the config. It has 3 devices (station-rtr, rtu-01
and eng-ws-01), 2 switches, 2 networks (WAN and STATION) and 4 connections.
The draft remembers the name of the file: with nothing selected, the
Inspector shows "Source file pump-station.topology.yaml" under **Details**
(see [With nothing selected](editor.md#with-nothing-selected)).

Importing an uploaded config needs the `configs` `create` permission. A
draft made from an uploaded config can publish a new topology. It cannot
update a stored config, even one with the same name (see
[Publishing](publishing.md)).

An uploaded Experiment config always keeps its own copy of its scenario, and
the import says so: "the uploaded experiment's copy of scenario
"riverside-water" is attached as an uploaded scenario, not this server's
stored scenario of that name".

phenix reads an uploaded config as it reads one created with
`phenix config create`. That includes `${NAME}` and `${NAME:default}`, which
phenix fills in from the environment of the phenix server. For example, this
interface in pump-station.topology.yaml:

```yaml
          - name: eth0
            type: ethernet
            vlan: WAN
            proto: static
            address: ${STATION_WAN_ADDRESS:198.51.100.20}
            mask: 24
            gateway: 198.51.100.1
```

imports with the address `198.51.100.20` when the server has no
`STATION_WAN_ADDRESS` variable.

!!! warning
    Anyone who may create configs can read the phenix server's environment
    variables this way, as with `phenix config create`. See
    [sandialabs/sceptre-phenix#436](https://github.com/sandialabs/sceptre-phenix/pull/436).

### What an import keeps

- Every node, as a device, with all of its settings: hardware, interfaces,
  routes, rulesets, labels, annotations, injections and the rest.
- One network, and one switch, for each VLAN the interfaces use. Each
  interface with a VLAN is connected to that network's switch. An interface
  without a VLAN is kept, but not connected.
- The devices of included topologies, read only (see
  [Included topologies](diagrams.md#included-topologies)). For
  riverside-water these are dns-01 and ntp-01 from corp-services.
- The annotations of the config itself, such as `maintainer: range-team`
  and `purpose: Water utility training range` on riverside-water. The
  Inspector shows them with nothing selected, under "From Topology
  riverside-water, imported" and the date. They are shown only: publishing
  does not write them. Annotations whose names start with `builder-` are
  left out. A draft keeps at most 100 annotations, and 256 KiB of them in
  all; the import warns about the rest.
- No positions: an import is always laid out on the **Default** grid.

A warning names anything else the import left out or changed.

Import makes a draft, which needs the `configs` `create` permission.
Importing a stored config also needs permission to read it. See
[Permissions](administration.md#permissions).

## Uploading a Builder document

A Builder document is Builder v2's own file: the diagram with all its
settings, positions, groups, notes and scenario. **Builder JSON** and
**Builder YAML** export one (see [Builder JSON and YAML](#builder-json-and-yaml)).
**Upload** opens a Builder document as a new draft. The draft you have open,
if any, does not change.

To upload the pump station as a Builder document, from
[pump-station.builder.json](examples/pump-station.builder.json):

1. Select **Upload**, on the drafts page or in the editor toolbar. The
   **Upload diagram** dialog opens.
2. Keep **Source** set to **File**. In **Builder document file**, choose
   `pump-station.builder.json`.
3. Select **Upload**. The editor opens a new draft named "Pump station",
   the name the document gives the diagram. With nothing selected, the
   Inspector shows where the draft came from under **Details**: "Source
   file pump-station.builder.json" (see
   [With nothing selected](editor.md#with-nothing-selected)).

The dialog in step 2:

![The Upload diagram dialog with Source File selected and pump-station.builder.json chosen as the Builder document file, and the Cancel and Upload buttons.](../images/builder-v2/upload-dialog.png)

The dialog has two other sources:

- **Paste text**: paste the document into **Document text (JSON or YAML)**,
  then select **Upload**. A draft made from pasted text has no source file.
- **Published diagram**: choose a diagram in **Published diagram** ("Select
  a diagram"), then select **Open**. This opens your draft of that published
  diagram, or makes one the first time, as **Edit as a draft** does (see
  [Published diagrams](drafts.md#published-diagrams)). A topology whose
  diagram is read from a file on the server is listed with "(File)" after
  its name.

**Upload** refuses a file it cannot use, and says why:

- A Topology or Experiment config, for example: "A Topology config is not a
  Builder document. Use Import on the drafts page so the server can convert
  it." Import it instead (see
  [Importing an uploaded config](#importing-an-uploaded-config)).
- A file that is not JSON or YAML, such as a GEXF file: "Could not parse the
  document: …".
- A file over 5 MiB: "The uploaded file is larger than the 5 MiB limit."

A Builder document keeps everything, so an export and an upload give the
same diagram. For example, export Riverside Water as **Builder YAML**, then
upload `riverside-water.yaml`: the new draft has the same 12 devices,
4 groups and note, the same **ELK layered** layout, and the same scenario.

A Builder document also says who made the diagram and who saved it last
(see [Who made and last saved a diagram](#who-made-and-last-saved-a-diagram)).
An uploaded draft keeps the author and the creation time the document
names, and you are the one who saved it last. The example file names
`e2e-admin` as its author. So after alice uploads it, the Inspector shows
these **Details**:

- **Created**: "Sep 29, 2026, 12:38 PM by e2e-admin", as the file says. The
  time is shown in the time zone of your browser, here US Mountain Time.
- **Last edited**: the time of the upload, and "by alice".
- **Source file**: "pump-station.builder.json".

A document that names no author gets you as its author, and the time of the
upload as its creation time.

Upload makes a draft, which needs the `configs` `create` permission.

## Exporting

To export the Riverside Water diagram:

1. Open the Riverside Water draft.
2. Select **Export** in the toolbar. The **Export diagram** dialog opens. It
   shows the size of the whole diagram: "Diagram bounds: 2384 × 1048 px".
3. Select a format. The browser saves the file, and the dialog says so, for
   example "Saved riverside-water.json."
4. Select **Close**.

![The Export diagram dialog with the diagram bounds, the Builder JSON, Builder YAML, Topology YAML, PNG, SVG and Gephi (GEXF) buttons, and the message that riverside-water.gexf was saved with 12 devices, 4 networks and 15 connections.](../images/builder-v2/export-dialog.png)

| Button | File for Riverside Water | What it holds | Open it with |
|---|---|---|---|
| **Builder JSON** | `riverside-water.json` | The whole Builder document | **Upload** |
| **Builder YAML** | `riverside-water.yaml` | The same, as YAML | **Upload** |
| **Topology YAML** | `riverside-water.topology.yaml` | The Topology config **Publish** would write | `phenix config create`, or **Import** as an uploaded config |
| **PNG** | `riverside-water.png` | A picture of the whole diagram | An image viewer |
| **SVG** | `riverside-water.svg` | The same picture, as SVG | A web browser |
| **Gephi (GEXF)** | `riverside-water.gexf` | The devices, networks and connections as a graph | Gephi |

The file name is the diagram name in lower case, with a hyphen for each run
of other characters than letters, digits, `.`, `_` and `-`. "Riverside Water"
gives `riverside-water`.

Export works in every draft you can open, including one you can only view,
and in a published diagram. Before the dialog opens, Builder v2 saves the
changes you made in the Inspector but did not apply. When it cannot apply
them, the dialog says why, for example "Your changes to Device ws-01 in the
Inspector cannot be exported until Hostname is fixed. Fix or cancel them
first.", and no export is made.

### Builder JSON and YAML

Builder JSON and Builder YAML hold the whole Builder document: every node
with its settings and position, the networks, the connections, the groups
and notes, the layout, the scenario, where the diagram was imported from,
and who made and last saved it. The example file
`riverside-water.builder.json` begins like this as YAML:

```yaml
$schema: https://phenix.sandia.gov/schemas/builder/v1
revision: 1
id: 35923065-de11-56bc-9e1d-a307308716a7
name: Riverside Water
description: 'Water utility training range: internet edge, DMZ, corporate and OT networks.'
author: e2e-admin
createdAt: '2026-09-29T18:17:59Z'
updatedBy: e2e-admin
updatedAt: '2026-09-29T18:31:44Z'
nodes:
  - id: 017b57e6-1d23-5555-a576-d0cdc682109a
    kind: device
    label: edge-rtr
```

Use them to keep a copy of a draft, to move a diagram to another phenix
server, or to hand it to someone. **Upload** opens them as a new draft (see
[Uploading a Builder document](#uploading-a-builder-document)), and
`phenix builder publish` makes a topology from them (see
[From the command line](#from-the-command-line)). The phenix
server describes the format as a JSON Schema at `/api/v1/schemas/builder-v2/v1`.

#### Who made and last saved a diagram

Four fields at the top of the document say who made the diagram and who
saved it last. The phenix server writes them each time it saves a draft;
the editor never does. The Inspector shows them under **Details** (see
[With nothing selected](editor.md#with-nothing-selected)).

| Field | What it holds | Set |
|---|---|---|
| `author` | The user who made the diagram | When a draft is created: the `author` the document already names, else the user who creates the draft. Every later save keeps it. |
| `createdAt` | When the diagram was made | With `author`, the same way |
| `updatedBy` | The user whose save stored this content | On every save: the user who saves |
| `updatedAt` | When that save was | On every save: the time of the save |

The times are in UTC, to the second, in the form `2026-09-29T18:17:59Z`. A
user name is at most 256 bytes. All four fields are optional: a document
written by hand may have none. Opened read only from a file, such a diagram
shows no **Details**.

What this means for each way of making a draft:

- **Blank diagram** and **Import**: you are the author, and the diagram is
  created now.
- **Upload**: the author and creation time the file names are kept. When the
  file names none, you are the author.
- **Edit as a draft** on a published diagram or on a diagram read from a
  file: the draft starts as that document, unchanged, so all four fields
  are the document's. Your first edit makes you the one who saved it last.
- A draft shared with **Can edit**: the author stays, and a save by the
  other person names that person in `updatedBy`.
- **Undo**, **Redo** and **Restore** go back to an earlier snapshot, which
  holds the `updatedBy` and `updatedAt` of the save that made it.

**Publish** and `phenix builder publish` store the document as it is, with
the four fields it has. They are part of the document, so they count in its
digest.

!!! note
    `author` and `createdAt` are what the document says. Someone who uploads
    a document can name any author in it. `updatedBy` and `updatedAt` are
    always written by the server.

`source.updatedAt`, further down in a document made by **Import**, is a
different time: when the imported config was last changed.

### Topology YAML

**Topology YAML** is the phenix Topology config that **Publish** would write
for the diagram. The phenix server makes it the way **Publish** does, and
checks it the same way. Nothing is written on the server.

The Riverside Water diagram exports as this Topology (the first node shown;
the file has ten):

```yaml
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
    name: Riverside-Water
spec:
    includeTopologies:
        - corp-services
    nodes:
        - annotations:
            vrouter/enable-ssh: eth2
          general:
            description: VyOS edge router
            hostname: edge-rtr
          hardware:
            drives:
                - image: vyos.qc2
            memory: 2048
            os_type: vyos
            vcpus: 2
          labels:
            zone: edge
          network:
            interfaces:
                - address: 203.0.113.2
                  gateway: 203.0.113.1
                  mask: 24
                  name: eth0
                  proto: static
                  type: ethernet
                  vlan: INTERNET
                - address: 172.16.10.1
                  mask: 24
                  name: eth1
                  proto: static
                  type: ethernet
                  vlan: DMZ
                - address: 10.10.20.1
                  mask: 24
                  name: eth2
                  proto: static
                  type: ethernet
                  vlan: CORP
            routes:
                - cost: 1
                  destination: 10.10.30.0/24
                  next: 10.10.20.254
          type: Router
```

What to note in the file:

- `metadata.name` is the topology name the Publish dialog proposes: the
  diagram name with a hyphen for each run of characters a config name
  cannot hold. "Riverside Water" gives `Riverside-Water`. Change it in the
  file to load the file under another name.
- The metadata holds no annotations: neither those of an imported config
  nor the one publishing adds to name the published diagram.
- The devices of included topologies (dns-01 and ntp-01) are not in
  `nodes`. `includeTopologies` names corp-services instead, as publishing
  does.
- Each interface's `vlan` is the name of the network it is connected to.
- Notes, groups, colors and positions are not in the file.

To store the file in phenix as a Topology config named `Riverside-Water`:

```bash
phenix config create riverside-water.topology.yaml
```

A topology stored this way is a plain Topology config: Builder v2 did not
publish it, so editing it from **Configs** does not open Builder v2. To
store the topology with its diagram instead, publish the Builder file with
`phenix builder publish` (see [From the command line](#from-the-command-line)),
or name the Builder file in the config (see
[A Builder file beside a topology](#a-builder-file-beside-a-topology)).

When the diagram cannot be published yet, the file is still saved, and the
dialog lists every reason. The Riverside Water expansion draft gives:
"Saved riverside-water-expansion.topology.yaml. This topology cannot be
published yet: interface "eth0" of device "historian-01-2" has no VLAN:
connect it to a network, or type a VLAN for it; IP address 10.10.30.20 is
used by interface "eth0" of device "historian-01" and interface "eth0" of
device "historian-01-2"."

![The Export diagram dialog after Topology YAML: riverside-water-expansion.topology.yaml was saved, and the topology cannot be published yet because eth0 of historian-01-2 has no VLAN and 10.10.30.20 is used by two interfaces.](../images/builder-v2/export-topology-blockers.png)

This makes **Topology YAML** a quick way to see every problem that
publishing would report (see
[What blocks publishing](publishing.md#what-blocks-publishing)). When
phenix could not accept the config at all, the dialog shows the error and no
file is saved.

Topology YAML needs the `configs` `get` permission.

### PNG and SVG

**PNG** and **SVG** draw the whole diagram, not only the part in view. The
picture covers the diagram bounds that the dialog shows, and is scaled to
at most 4096 pixels on its longer side. The Riverside Water PNG is 4096 ×
1801 pixels.

The picture leaves out what is only there for editing: the selection, the
connection points and the warning marks. Its background follows the theme:
white in the light theme, dark in the dark theme (see
[Themes](editor.md#themes)).

The SVG holds the diagram as HTML inside the SVG. Web browsers show it; some
drawing programs cannot.

### Gephi (GEXF)

**Gephi (GEXF)** saves the network as a graph for
[Gephi](https://gephi.org/) and other tools that read GEXF 1.3. Builder v2
cannot open this file.

The graph has:

- A node for each device, and one for each network. All the switches of a
  network are one node.
- An edge for each connection, from the device to its network. The edge
  label is the interface and its address, for example "eth0
  10.10.30.20/24".
- The colors and positions of the diagram. Colors written in hex or `rgb()`
  are kept; a color written as a name, such as `red`, is not.

Notes and groups are not nodes. A node's groups are in its **Group** and
**Groups** columns instead.

Each node and edge carries columns with its settings. A column is in the
file only when some node or edge has a value for it. For Riverside Water:

- Node columns: **Node kind** (device or switch), **Hostname**,
  **Description**, **Device type**, **OS type**, **vCPUs**, **Memory (MB)**,
  **Disk images**, **External**, **Icon**, **Included from**, **Group**,
  **Groups**, **Interfaces**, **Interfaces (text)**, **Interface count**,
  **VLANs**, **VLANs (text)**, **IP addresses**, **IP addresses (CIDR)**,
  **Gateways**, **Routes**, **Route count**, **Rulesets**, **Injection
  count**, **Scenario apps**, **Scenario apps (text)**, **Scenario app
  count**, **Labels**, **Labels (text)**, **Network** and **Attached
  interfaces** (for networks), and one column for each label and annotation
  name, such as **Label: zone** and **Annotation: vrouter/enable-ssh**.
- Edge columns: **Device**, **Network**, **Interface**, **Interface type**,
  **Protocol**, **IP address**, **Prefix length**, **IP address (CIDR)**,
  **Gateway**, **Ruleset in**, **QinQ** and **Autostart**.

Other diagrams can have more, such as **MAC addresses**, **VLAN alias** and
**Disabled scenario apps**.

The **Scenario apps** columns list the apps of the diagram's scenario that
run on each device: historian-01 has `ntp`. For a stored scenario, Export
reads the scenario from the server first. When it cannot, the file has no
app columns, and the dialog says why, for example "It lists no scenario
apps: your role cannot read scenario riverside-water."

The dialog counts what it saved: "Saved riverside-water.gexf: 12 devices, 4
networks and 15 connections."

#### Opening the file in Gephi

These steps use the menu names of Gephi 0.10 and 0.11.

1. In Gephi, select **File** > **Open**, and choose `riverside-water.gexf`.
2. The **Import report** shows **# of Nodes:** 16 (12 devices and 4
   networks), **# of Edges:** 15 and **Graph Type:** Undirected. Select
   **OK**.
3. Select **Data Laboratory** to see the columns of the nodes and edges.

#### Filtering by network or address

To show only the connections on the OT network:

1. In **Overview**, open the **Filters** panel.
2. In **Library**, open **Attributes** > **Equal**.
3. Drag the edges' **Network** column (listed as "Network String (Edge)")
   into **Queries**.
4. Type `OT` in its field, then select **Filter**.

To show the connections with an address in 10.10.30.0/24, use the edges'
**IP address** column instead, select **Use regex**, and type
`10\.10\.30\..*`. The expression must match the whole value.

To find devices by VLAN, use the nodes' **VLANs (text)** column. It holds
the VLANs of a device separated by `|`, for example `INTERNET|DMZ|CORP` for
edge-rtr. Select **Use regex** and type `(.*\|)?OT(\|.*)?` to keep the
devices on OT and the OT network itself.

## From the command line

`phenix builder publish` makes a Topology config from a Builder document
file (Builder JSON or Builder YAML), without the web UI. It checks the
document as **Publish** does, stores the topology, and stores the document
with it as the topology's published diagram. The topology then opens in
Builder v2 like one published there: it is listed under **Published
Diagrams**, and **Edit as a draft** works on it.

```bash
phenix builder publish </path/to/document> [--name <topology>] [--update] [--dry-run] [--user <user>] [--record-path]
```

The command takes one file. It writes to the phenix store itself, as
`phenix config create` does, so run it where the other `phenix` commands
run. It works whether or not the `builder-v2` feature is on. With Docker,
the file must be where the container can read it, for example below
`/phenix`:

```bash
docker exec phenix phenix builder publish /phenix/topologies/pump-station/pump-station.builder.json
```

### Creating a topology

To publish the example file
[pump-station.builder.json](examples/pump-station.builder.json):

```console
$ phenix builder publish pump-station.builder.json
2026-10-01 21:51:06.342 INF topology created type=SYSTEM name=Pump-station document=1e13fa9bd9417c696159a9d0f1496953909516221c6600383cea29fa1fa8dc90 digest=sha256:5bbc6d046a1b98011f227ded600b90947bd1f44be35858654b4cae6b4cca9184
```

The topology is named after the diagram, the way the Publish dialog
proposes a name: "Pump station" gives `Pump-station`. `--name` (or `-n`)
sets another name, which must be a valid config name:

```bash
phenix builder publish pump-station.builder.json --name pump-lab
```

The new Topology config names the stored document in its `builder-doc`
annotation (see
[The builder-doc annotation](administration.md#the-builder-doc-annotation)):

```console
$ phenix config get topology/Pump-station
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
    name: Pump-station
    created: "2026-10-01T21:51:06-06:00"
    updated: "2026-10-01T21:51:06-06:00"
    annotations:
        builder-doc:
            digest: sha256:5bbc6d046a1b98011f227ded600b90947bd1f44be35858654b4cae6b4cca9184
            id: 1e13fa9bd9417c696159a9d0f1496953909516221c6600383cea29fa1fa8dc90
spec:
    nodes:
        - general:
            description: Field engineering laptop
            hostname: eng-ws-01
```

The output goes on with the rest of the three nodes: eng-ws-01, rtu-01 and
station-rtr.

Publishing the same document again changes nothing, so a script can run the
command each time:

```console
$ phenix builder publish pump-station.builder.json
2026-10-01 21:51:06.527 INF topology already up to date type=SYSTEM name=Pump-station document=1e13fa9bd9417c696159a9d0f1496953909516221c6600383cea29fa1fa8dc90 digest=sha256:5bbc6d046a1b98011f227ded600b90947bd1f44be35858654b4cae6b4cca9184
```

The command exits with 0 when the topology was created, updated or already
up to date, and with 1 otherwise. Results and warnings are log lines on
standard error; only `--dry-run` writes to standard output.

### Checking without writing

`--dry-run` runs every check and reports what the command would do. It
writes nothing. Here the example file is in
`/phenix/topologies/riverside-water`, and the
[example configs are loaded](index.md#load-the-example-configs):

```console
$ cd /phenix/topologies/riverside-water
$ phenix builder publish riverside-water.builder.json --dry-run
Document:     Riverside Water
File:         /phenix/topologies/riverside-water/riverside-water.builder.json
Digest:       sha256:82a1aba006d86a043d3d0ed615a7aa05aeff52407f1121f6e9224595c5dea308
Document ID:  85cabd30f51263863fb0282819aa4a88417556761a83b8a643f1d31be033df68
Topology:     Riverside-Water (would be created)
Nodes:        10
Warnings:
  - The document's scenario is not published: only the topology is.
Nothing was written.
```

The **Topology** line says "(would be created)", "(would be updated)" or
"(would be left as it is: it already holds this document)". A document that
cannot be published prints the reason as an error instead, and the command
exits with 1. This makes `--dry-run` a check for a repository's CI.

### Updating a topology

The command replaces an existing topology only with `--update`:

```console
$ phenix builder publish pump-station.builder.json
Error: topology Pump-station already exists; use --update to replace it
$ phenix builder publish pump-station.builder.json --update
2026-10-01 21:51:06.957 INF topology updated type=SYSTEM name=Pump-station document=0e98ffde0792cd8fd6b1e5e16ff0b865978ae5b605434125d54f6a3382ce8e43 digest=sha256:53953338b10e2e824be1f4da08fc9178026d89dfef63cc7adfd406c0a918468d
```

Here the file was changed after it was first published: the description of
rtu-01 is now "Remote terminal unit, pump 2". An update keeps the topology's
other annotations.

`--update` never discards someone else's change. It replaces the topology
in two cases only:

- The topology is still exactly what its stored published document
  publishes: nothing changed it since Builder v2 or this command published
  it.
- The document was made from the topology as it is now: imported from it in
  Builder v2 and exported, with the topology unchanged since. The example
  file `riverside-water.builder.json` was made from `riverside-water` this
  way, so this works after you
  [load the example configs](index.md#load-the-example-configs):

    ```console
    $ phenix builder publish riverside-water.builder.json --name riverside-water --update
    2026-10-01 21:51:07.132 WRN The document's scenario is not published: only the topology is. type=SYSTEM topology=riverside-water
    2026-10-01 21:51:07.132 INF topology updated type=SYSTEM name=riverside-water document=ae78c07ebf467ded1928a0f144b109fa47416e549c901fe0dd54f7753932e9b5 digest=sha256:82a1aba006d86a043d3d0ed615a7aa05aeff52407f1121f6e9224595c5dea308
    ```

!!! note
    This update changes `riverside-water`, as a publish from a draft does.
    Drafts made from `riverside-water` before, such as Riverside Water on
    these pages, can then no longer publish (see
    [When the source config changed](publishing.md#when-the-source-config-changed)).
    Add `--dry-run` to try the command without changing the topology.

In every other case the command refuses:

| Error | Why | What to do |
|---|---|---|
| `topology Pump-station was changed after it was published, and replacing it would discard that change` | The topology was changed after its document was published, with `phenix config edit` for example | Import the topology in Builder v2 and publish that draft, or delete the topology and publish the file again |
| `topology corp-services was not published from a Builder document that is still stored, and this document was not made from the topology as it is now` | The topology has no stored published document (a plain topology, or one that only names a Builder file), and the document was not imported from it as it is now | The same |
| `topology old-lab belongs to the legacy XML Builder and cannot be replaced by a Builder document` | The legacy Builder made the topology | Publish under another name |

There is no flag to force an update.

### What blocks publishing

The command refuses what **Publish** refuses (see
[What blocks publishing](publishing.md#what-blocks-publishing)), and lists
every problem at once:

```console
$ phenix builder publish blocked.builder.json
Error: the document cannot be published as topology Pump-station:
  interface "eth0" of device "eng-ws-01" has no VLAN: connect it to a network, or type a VLAN for it
  IP address 10.40.1.10 is used by interface "eth0" of device "rtu-01" and interface "eth1" of device "station-rtr"
```

Here `blocked.builder.json` is the pump station with eth0 of eng-ws-01
disconnected, and with the address of rtu-01 given to eth1 of station-rtr
too.

A file that is not a valid Builder document is refused with the reason, for
example for a Topology config:

```console
$ phenix builder publish pump-station.topology.yaml
Error: pump-station.topology.yaml is not a valid Builder document: decoding builder document: json: unknown field "apiVersion"
```

The file can be at most 5 MiB. Its content decides whether it is read as
JSON or YAML, not its name. `${NAME}` in the file is not filled in from the
environment.

### What the command does not do

- **Scenarios and experiments.** It writes a Topology config only. A
  document with a scenario publishes its topology, with the warning "The
  document's scenario is not published: only the topology is." To publish
  an experiment and its scenario, use **Publish** in Builder v2, or create
  the experiment from the topology with `phenix experiment create`.
- **VLAN aliases.** A topology holds none, so a network's **VLAN alias** is
  not written: "The document's VLAN alias is not published: a topology
  holds none."
- **Included topologies from files.** An included topology is checked for
  duplicate hostnames only when it is stored in phenix. Otherwise the
  command warns, for example "Included topology corp-services was not
  checked for duplicate hostnames: no stored topology has that name."
- **Drafts.** The command makes no draft. To edit the diagram, open the
  topology in Builder v2 and select **Edit as a draft**.

The document is stored as the file holds it. The command does not change
who made or last saved the diagram (see
[Who made and last saved a diagram](#who-made-and-last-saved-a-diagram)). It
records who published: the user that `--user` names, else the user who ran
`sudo`, else the operating system account that ran the command. In the
Docker deployment that is `root`. The name is a record only: it gives no
permission.

### With a running phenix server

The command writes to the phenix store itself, as `phenix config create`
does. It does not talk to the running `phenix ui`, so:

- A web page that is already open shows the new topology after a reload.
- Publishing the same topology from the command line and from Builder v2
  at the same moment is not coordinated: the config that is written last
  wins. Nothing is damaged, and the other published document is removed
  later.
- A Builder v2 draft that was made from the topology before the command
  changed it can no longer publish (see
  [When the source config changed](publishing.md#when-the-source-config-changed)).
- When the `builder-v2` feature is off, the command still works. The
  topology then has a `builder-doc` annotation, so the **Configs** page
  does not open it for editing as text and says it was built by Builder v2.
  `phenix config edit` still edits it.

### Recording the file's path

`--record-path` also writes the absolute path of the file into the
annotation, as `path`. Here no topology `pump-station` exists yet:

```console
$ cd /phenix/topologies/pump-station
$ phenix builder publish pump-station.builder.json --name pump-station --record-path
2026-10-01 21:51:22.878 INF topology created type=SYSTEM name=pump-station document=b856fc9e35107594f72e715f74ee1cae3eb951b41bf09a504327924e0a220d34 digest=sha256:5bbc6d046a1b98011f227ded600b90947bd1f44be35858654b4cae6b4cca9184
```

```yaml
    annotations:
        builder-doc:
            digest: sha256:5bbc6d046a1b98011f227ded600b90947bd1f44be35858654b4cae6b4cca9184
            id: b856fc9e35107594f72e715f74ee1cae3eb951b41bf09a504327924e0a220d34
            path: /phenix/topologies/pump-station/pump-station.builder.json
```

On this server the topology shows the stored document. The path matters
when the Topology config is used where that document is not stored, for
example on another phenix server with the same files: the diagram is then
read from the file (see
[Builder documents in files](administration.md#builder-documents-in-files)).

The file name must end in `.json`, `.yaml` or `.yml`. phenix reads Builder
files only below its base directory, so for a file elsewhere the command
still publishes, and warns:

```console
$ cd /home/alice
$ phenix builder publish pump-station.builder.json --name pump-home --record-path
2026-10-01 21:51:08.761 WRN The phenix server does not read Builder files from /home/alice/pump-station.builder.json: it reads them below /phenix, except below /phenix/mounts. The topology opens from the stored document. type=SYSTEM topology=pump-home
2026-10-01 21:51:08.761 INF topology created type=SYSTEM name=pump-home document=7ec10c84ea722c79c705731e516ea400dc6a18312de3f50aaafc55b7344abc3d digest=sha256:5bbc6d046a1b98011f227ded600b90947bd1f44be35858654b4cae6b4cca9184
```

A later `--update` from another file keeps the recorded path, and warns
that the file it names was not changed: "Topology pump-station names the
Builder file /phenix/topologies/pump-station/pump-station.builder.json,
which was not read or changed. Replace that file with the published document
to keep it in step." `--update --record-path` replaces the recorded path.

### Builder documents and phenix config create

A Builder document is not a config. `phenix config create` refuses one named
on the command line, and skips one it finds in a directory:

```console
$ phenix config create pump-station.builder.json
Error: pump-station.builder.json is a Builder document, not a configuration: use "phenix builder publish pump-station.builder.json" to create its topology
$ phenix config create examples
2026-10-01 21:51:05.923 INF configuration created type=SYSTEM kind=Topology name=corp-services
2026-10-01 21:51:05.962 INF configuration created type=SYSTEM kind=Topology name=metro-campus
2026-10-01 21:51:05.962 INF skipped Builder document; use phenix builder publish type=SYSTEM path=examples/pump-station.builder.json
2026-10-01 21:51:05.994 INF configuration created type=SYSTEM kind=Topology name=pump-station
2026-10-01 21:51:05.994 INF skipped Builder document; use phenix builder publish type=SYSTEM path=examples/riverside-water.builder.json
2026-10-01 21:51:06.023 INF configuration created type=SYSTEM kind=Scenario name=riverside-water
2026-10-01 21:51:06.057 INF configuration created type=SYSTEM kind=Topology name=riverside-water
2026-10-01 21:51:06.086 INF configuration created type=SYSTEM kind=Role name=topology-designer
2026-10-01 21:51:06.115 INF configuration created type=SYSTEM kind=Role name=topology-reviewer
```

Here `examples` is a directory with every
[example file](index.md#load-the-example-configs) of these pages.

## A Builder file beside a topology

A topology that lives in a repository can keep its diagram there too, as a
Builder JSON or Builder YAML file next to the Topology config. Name the
file in the config's `builder-doc` annotation:

```yaml
metadata:
    name: pump-station
    annotations:
        builder-doc:
            path: /phenix/topologies/pump-station/pump-station.builder.json
```

After `phenix config create`, the **Published Diagrams** tab lists the
topology with the tag **File**, and its diagram opens from the file, with
nothing published first. The file must be on the phenix server, below
`/phenix`. See
[Builder documents in files](administration.md#builder-documents-in-files)
for the example in full, the rules, and what **Publish** does with such a
topology.
