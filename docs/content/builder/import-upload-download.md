# Import, Upload and Download

Builder can start a draft from a phenix config or from a file that you
have. It can save a diagram as a file in seven formats. These pages use
three words for these tasks:

- **Import** makes a draft from a phenix config: a Topology or Experiment
  config stored in phenix, or a config file. The phenix server converts the
  config into a diagram.
- **Upload** opens a file that is already a diagram: a Builder document
  (such as a draft that you downloaded), a Builder package, or a diagram of
  the [legacy Builder](legacy.md). Its **Published diagram** source opens a
  diagram published on the server.
- **Download** saves the open diagram as a file.

None of these tasks changes a config on the phenix server. In the web UI,
only **Publish** writes configs (see [Publishing](publishing.md)).

| To | Use | Where | Result |
|---|---|---|---|
| Start from a Topology or Experiment config | **Import** | Drafts page, and the **Configs** page | A new draft |
| Open a Builder document, a Builder package, a published diagram, or a legacy Builder diagram | **Upload** | Drafts page and editor toolbar | A new draft |
| Move a diagram to another phenix server with what it needs | **Download** a **Builder package**, then **Upload** it there | Editor toolbar, then the drafts page | A file, then a new draft (see [Moving a diagram with a Builder package](#moving-a-diagram-with-a-builder-package)) |
| Save the diagram as a file | **Download** | Editor toolbar | A file |
| Show the diagram of a topology kept in files | A `builder-doc` annotation with a `path` | The Topology config | A card under **Published Diagrams** (see [A Builder file beside a topology](#a-builder-file-beside-a-topology)) |

The examples on this page use the [example lab](index.md#the-example-lab).

## Importing a topology or experiment

**Import** makes a draft from a Topology or Experiment config. The config can
be one stored in phenix, or a config file. The phenix server converts the
config, so the draft holds what phenix would read from it.

### From a stored config

To import the riverside-water topology:

1. On the drafts page, select **Import**. The **Import topology or
   experiment** dialog opens.
2. Keep **Source** set to **Stored config**, and **Source kind** set to
   **Topology**.
3. In **Source name**, choose `riverside-water`. Because riverside-water
   includes corp-services, the dialog shows **Included topologies**:
   "riverside-water includes 1 other topology." Keep
   **Keep included nodes read only**, and leave
   **Create a new topology as a copy** clear (see
   [Import options](#import-options)).
4. Select **Import**. The dialog says "This import has 1 warning." and lists
   it: "Added 2 nodes from included topology corp-services (2 nodes). They
   are shown read only: edit them in their own topology. Publishing keeps
   includeTopologies instead of copying them."
5. Select **Continue to editor**.

The dialog in step 3:

![The Import topology or experiment dialog with Stored config, Source kind Topology and Source name riverside-water selected, Included topologies set to Keep included nodes read only, Create a new topology as a copy clear, and the Cancel and Import buttons.](../images/builder/quickstart-import-dialog.png)

The editor opens a new draft named riverside-water. The header counts 12
devices, 4 switches, 4 networks and 15 connections. The layout menu says
**Default**: the devices are in rows on a grid, with the switches below
them. Choose a layout to arrange them (see [Layouts](diagrams.md#layouts)).

To stop at step 5, select **Cancel**. The dialog closes and no draft is
made. An import without warnings opens the editor immediately, without
step 5. Until the editor opens, the diagram that was open before does not
change. If you close the dialog while it says "Importing…", nothing is
imported, even when the answer comes later.

A topology that the [legacy Builder](legacy.md) saved is marked
"(legacy Builder diagram)" in **Source name**. Importing it converts its
diagram, and publishing the draft to the same topology replaces the legacy
diagram (see
[Converting a stored topology](legacy.md#converting-a-stored-topology)).

### Importing an experiment

An experiment imports the same way. To import the riverside experiment:

1. Select **Import**.
2. Keep **Stored config**. Set **Source kind** to **Experiment**, and choose
   `riverside` in **Source name**.
3. Select **Import**. The dialog says "This import has 2 warnings."
4. Select **Continue to editor**. The new draft is named riverside.

The warnings in step 3:

![The import of experiment riverside with two warnings: the two nodes from the included topology corp-services are shown read only, and the experiment fields baseDir, defaultBridge and deployMode are not kept; with Cancel and Continue to editor.](../images/builder/import-experiment-warnings.png)

An imported experiment differs from an imported topology in these ways:

- When your role can list the stored Scenario that the experiment names,
  the draft lists it. The Inspector says "Scenario riverside-water".
  Otherwise the draft lists no scenario, and the import warns: "the
  experiment's scenario "riverside-water" is not a stored Scenario config
  and was not attached". The import never keeps the copy of the scenario
  that the experiment holds, because a diagram names its scenarios and
  holds none.
- The experiment's VLAN aliases become the **VLAN alias** of each network.
- Some experiment fields have no place in a diagram. The import names them,
  for example "experiment fields not represented in the builder document:
  baseDir, defaultBridge, deployMode". The experiment's VLAN range is not
  kept either.
- The files that the apps of the experiment injected into VMs when it
  started are left out, without a warning. The injections of the topology
  are kept.

### Importing a config file

A config file does not need to be stored in phenix first. To import
the pump station of the example lab from
[pump-station.topology.yaml](examples/pump-station.topology.yaml):

1. Select **Import**.
2. Set **Source** to **Config file**.
3. In **Topology or Experiment config**, choose `pump-station.topology.yaml`.
   The file can be JSON or YAML, up to 5 MiB.
4. Select **Import**.

This config has no warnings, so the editor opens immediately. The new draft
is named pump-station, after the config. It has 3 devices (station-rtr, rtu-01
and eng-ws-01), 2 switches, 2 networks (WAN and STATION) and 4 connections.
The draft remembers the name of the file: with nothing selected, the
Inspector shows "Source file pump-station.topology.yaml" under **Details**
(see [With nothing selected](editor.md#with-nothing-selected)).

A draft made from a config file cannot update a stored config, even one
with the same name. It can only publish a new topology (see
[Publishing](publishing.md)). For this reason, the dialog does not offer a
copy for a config file. It does offer **Included topologies** when the
topology in the file includes others (see [Import options](#import-options)).
The file must name its config in `metadata.name`.

For an Experiment config file, the import finds the scenario in the same
way: by name, among the stored Scenarios that your role can list. When the
draft lists the scenario, the import says so, because the copy in the file
can be different: "scenario "riverside-water" is this server's Scenario
config of that name, not the copy the experiment file holds".

phenix reads a config file as it reads one created with
`phenix config create`. This includes `${NAME}` and `${NAME:default}`, which
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

### Import options

When they apply, the dialog shows more choices after the source. An
Experiment has none.

**Included topologies** shows when the topology includes other topologies
(its `includeTopologies`), stored or in a config file. The text above the
choices says how many, for example "riverside-water includes 1 other
topology.":

- **Keep included nodes read only** (the default): "Their nodes are shown
  but cannot be changed here. Publishing keeps the includes." This is the
  import described above (see
  [Included topologies](diagrams.md#included-topologies)).
- **Combine into one new topology**: "Their nodes are copied into the
  diagram and can be changed. Publishing creates a new topology." For
  riverside-water the import warns: "Copied 2 nodes from included topology
  corp-services (2 nodes). They are ordinary nodes of this diagram now:
  changes here do not reach that topology, and later changes there do not
  reach this diagram." The import does not combine an included topology that
  you may not read, or that is not stored in phenix. The draft still
  includes it, and the import warns, for example "Included topology site-b was not combined
  and stays in includeTopologies: publishing keeps the reference."

**Create a new topology as a copy** shows for a stored topology, while
**Combine into one new topology** is not chosen (combining makes a new
topology already). Its hint says "The draft is not linked to
riverside-water. Publishing creates a new topology and leaves
riverside-water as it is." Included nodes stay read only.

With either option, **New topology name** asks for the name of the new
topology. It proposes the name of the topology with `-copy` or `-combined`,
such as `riverside-water-copy`. When a topology already has that name, it
adds a number. The name follows the config name rule (see
[Publishing a topology](publishing.md#publishing-a-topology)), can be at most
512 bytes long, and cannot be the name of an existing topology: "A topology
named riverside-water already exists. Enter another name."

A copy, or a combined draft, is named after the new topology and is linked
to no config. It gets a diagram note that names the config it was copied
from, for example "Copied from riverside-water". For a config file, the
note gives the name in the `metadata` of the file. The Inspector shows the note
under **Notes** when nothing is selected (see
[With nothing selected](editor.md#with-nothing-selected)). You can change or
delete it like any other note.

**Publish** proposes the new name and creates a new topology ("A new
topology will be created."). It cannot update the topology it came from,
and later changes to that topology, or to the topologies combined into it,
do not reach the draft.

To make the included nodes of a draft you already have editable, without
importing again, use **Combine included nodes into a new draft** (see
[Included topologies](diagrams.md#included-topologies)).

### From the Configs page

The **Configs** page links each topology to the Builder. A topology with a
Builder diagram has the tag `builder`. A topology that the legacy Builder
saved has the tag `builder legacy`. The tag is a link. When you select the
name of a topology, a viewer opens, with a button to the left of
**Edit Config**:

| Topology | Tag (its tooltip) | Viewer button | What opens |
|---|---|---|---|
| Has a Builder diagram (`builder-doc`) | `builder` ("open in Builder") | **Open in Builder** | The diagram, in a draft (see [Editing a published topology](publishing.md#editing-a-published-topology)) |
| Has a legacy Builder diagram (`builder-xml`) | `builder legacy` ("import into Builder") | **Import into Builder** | The **Import** dialog, set to the topology: "Topology riverside-water has a legacy Builder diagram. Import it to convert the diagram." |
| Has no diagram | None | **Import into Builder** | The **Import** dialog, set to the topology: "Topology riverside-water has no Builder diagram to open. Import it to make one." |

The dialog makes nothing until you select **Import**, and offers the
[import options](#import-options).

The tag is a link for a role with `configs` `list` and `configs` `get` on
the topology. The import controls also need `configs` `create`. Without it,
the tag is plain text and the viewer has no button. When the Builder cannot
continue, it gives the reason: "Topology riverside-water does not exist, or you may not
read it.", or "Topology riverside-water has no Builder diagram, and your
role cannot create drafts to import it. Select its name in Configs to view
it."

The edit button of a `builder` topology, and **Edit Config** in its viewer,
open the Builder too. Any other topology, `builder legacy` included, opens
in the text editor (see [Legacy Builder](legacy.md#what-changed)).

### What an import keeps

- Every node, as a device, with all of its settings: hardware, interfaces,
  routes, rulesets, labels, annotations, injections and the rest.
- One network, and one switch, for each VLAN that the interfaces use. Each
  interface with a VLAN is connected to the switch of that network. An
  interface without a VLAN is kept, but not connected.
- The devices of included topologies, read only (see
  [Included topologies](diagrams.md#included-topologies)), unless you
  combine them (see [Import options](#import-options)). For
  riverside-water these are dns-01 and ntp-01 from corp-services.
- The annotations of the config itself, such as `maintainer: range-team`
  and `purpose: Water utility training range` on riverside-water. The
  Inspector shows them with nothing selected, under "From Topology
  riverside-water, imported" and the date. They are for display only:
  publishing does not write them. Annotations whose names start with
  `builder-` are left out. A draft keeps at most 100 annotations, and
  256 KiB of them in total. The import warns about the others.
- No positions: an import always uses the **Default** grid layout.

A warning names anything else the import left out or changed.

Import makes a draft, which needs the `configs` `create` permission.
Importing a stored config also needs permission to read it. See
[Permissions](administration.md#permissions).

## Uploading a Builder document

A Builder document is the file format of Builder: the diagram with all its
settings, positions, groups, notes and the names of its scenarios.
**Download** saves one as **Builder JSON** or **Builder YAML** (see
[Builder JSON and YAML](#builder-json-and-yaml)). **Upload** opens a
Builder document as a new draft. The draft that is open, if any, does not
change.

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

![The Upload diagram dialog with Source File selected and pump-station.builder.json chosen as the Builder document file, and the Cancel and Upload buttons.](../images/builder/upload-dialog.png)

The dialog has three other sources:

- **Paste text**: paste the document into **Document text (JSON or YAML)**,
  then select **Upload**. A draft made from pasted text has no source file.
- **Published diagram**: choose a diagram in **Published diagram** ("Select
  a diagram"), then select **Open**. This opens the same draft as
  **Edit as a draft** (see
  [Published diagrams](drafts.md#published-diagrams)): the draft that
  published the diagram, when it is yours or shared with you to edit.
  Otherwise it opens your draft of the diagram, or makes one the first
  time. A topology whose diagram is read from a file on the server shows
  "(File)" after its name.
- **Legacy Builder diagram or Topology**: choose a diagram file that the
  legacy Builder saved, or a Topology config file that holds one in its
  `builder-xml` annotation, in **Legacy diagram or Topology file**, then
  select **Convert**. The server converts the diagram into a new draft. See
  [Converting a file](legacy.md#converting-a-file).

**Upload** refuses a file it cannot use, and says why:

- A Topology or Experiment config, for example: "A Topology config is not a
  Builder document. Use Import on the drafts page so the server can convert
  it." Import it instead (see
  [Importing a config file](#importing-a-config-file)).
- A file that is not JSON or YAML: "Could not parse the document: …".
- An XML file, such as a legacy Builder diagram or a GEXF file: "This looks
  like a legacy Builder diagram (XML). Choose "Legacy Builder diagram or
  Topology" to convert it." A GEXF file cannot be converted either.
- A file over 5 MiB: "The uploaded file is larger than the 5 MiB limit."

A Builder document keeps everything, so a download and an upload give the
same diagram. For example, download Riverside Water as **Builder YAML**, then
upload `riverside-water.yaml`: the new draft has the same 12 devices,
4 groups and note, the same **ELK layered** layout, and the same scenario
`riverside-water` listed.

An uploaded draft keeps the maker (`createdBy`) and the creation time that
the document names, and you become the last editor (see
[Who made and last saved a diagram](#who-made-and-last-saved-a-diagram)).
For example, the example file names `e2e-admin` as its maker. After alice
uploads it, the Inspector shows these **Details**:

- **Created**: "Sep 29, 2026, 12:38 PM by e2e-admin", in the time zone of
  the browser.
- **Last edited**: the time of the upload, and "by alice".
- **Source file**: "pump-station.builder.json".

Upload makes a draft, which needs the `configs` `create` permission.

## Downloading

To download the Riverside Water diagram:

1. Open the Riverside Water draft.
2. Select **Download** in the toolbar. The **Download diagram** dialog opens.
   It shows the size of the whole diagram: "Diagram bounds: 2384 × 1048 px".
3. Select a format. The buttons are in two rows: **Builder JSON**,
   **Builder YAML** and **Topology YAML**, then **PNG**, **SVG** and
   **Gephi (GEXF)**. The browser saves the file, and the dialog says so, for
   example "Saved riverside-water.json." Below the buttons,
   **Builder package** saves the diagram with what it needs (see
   [Downloading a package](#downloading-a-package)).
4. Select **Close**.

![The Download diagram dialog with the diagram bounds, the Builder JSON, Builder YAML and Topology YAML buttons in one row and the PNG, SVG and Gephi (GEXF) buttons in the next, their hints, the Builder package part with its hint, the Scenario configs, Included topologies, Custom icons and Disk-image requirements checkboxes not ticked, Package format JSON and the Builder package button, and the message that riverside-water.gexf was saved with 12 devices, 4 networks and 15 connections.](../images/builder/download-dialog.png)

The command palette has a command for each format, such as
**Download PNG** (see [Command palette](editor.md#command-palette)).

| Button | File for Riverside Water | What it holds | Open it with |
|---|---|---|---|
| **Builder JSON** | `riverside-water.json` | The whole Builder document | **Upload** |
| **Builder YAML** | `riverside-water.yaml` | The same, as YAML | **Upload** |
| **Topology YAML** | `riverside-water.topology.yaml` | The Topology config **Publish** would write | `phenix config create`, or **Import** as a config file |
| **PNG** | `riverside-water.png` | A picture of the whole diagram | An image viewer |
| **SVG** | `riverside-water.svg` | The same picture, as SVG | A web browser |
| **Gephi (GEXF)** | `riverside-water.gexf` | The devices, networks and connections as a graph | Gephi |
| **Builder package** | `riverside-water.package.json` or `.package.yaml` | The Builder document, the configs and icons you tick, and the list of what the diagram needs | **Upload** |

The file name is the diagram name in lower case. Each run of characters
other than letters, digits, `.`, `_` and `-` becomes one hyphen.
"Riverside Water" gives `riverside-water`.

Download works in every draft that you can open, including one that you can
only view, and in a published diagram. Before the dialog opens, Builder
saves the changes that you did not apply in the Inspector (see
[Automatic snapshots](drafts.md#automatic-snapshots)). When it cannot apply
them, the dialog gives the reason and saves no file, for example "Your
changes to Device ws-01 in the Inspector cannot be downloaded until
Hostname is fixed. Fix or cancel them first."

### Builder JSON and YAML

Builder JSON and Builder YAML hold the whole Builder document:

- Every node with its settings and position, the networks, the
  connections, the groups and notes, and the layout.
- The names of its scenarios (`scenarios`), and where the diagram was
  imported from.
- Under `metadata`, the name, description and notes of the diagram, and who
  made and last saved it.
- How the diagram looks: the colors and line styles of nodes and
  connections, and the description, border pattern and icon of each group.
- The device templates of the diagram (`templates`).
- A copy of each custom icon that the diagram names (`icons`), so the file
  opens the same on another phenix server (see
  [The diagram and its icons](diagrams.md#the-diagram-and-its-icons)).

The example file `riverside-water.builder.json` begins like this as YAML:

```yaml
$schema: https://phenix.sandia.gov/schemas/builder/v1
revision: 1
metadata:
  id: 35923065-de11-56bc-9e1d-a307308716a7
  name: Riverside Water
  description: 'Water utility training range: internet edge, DMZ, corporate and OT networks.'
  createdBy: e2e-admin
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
[Uploading a Builder document](#uploading-a-builder-document)). The phenix
server describes the format as a JSON Schema at `/api/v1/schemas/builder/v1`,
with a title, a description and examples for every field.

#### The metadata of a diagram

The `metadata` object at the top of the document holds what the document
says about itself. It is required, and it holds only these fields:

| Field | What it holds |
|---|---|
| `id` | The identifier of the document. Every document needs one |
| `name` | The diagram name, at most 512 bytes. A draft uses it as its title |
| `description` | Free text about the diagram |
| `createdBy`, `createdAt`, `updatedBy`, `updatedAt` | Who made the diagram and who saved it last (see below) |
| `notes` | The notes of the diagram, which the Inspector lists under **Notes**. At most 100 notes. Each note is not blank, is at most 4096 bytes, and has no control characters other than line breaks and tabs |

The server refuses a document that has one of these fields at its top
level, outside `metadata`. It also refuses a field that `metadata` does not
list. Publish writes none of the metadata to a config.

#### Who made and last saved a diagram

Four fields of the document's `metadata` say who made the diagram and who
saved it last. The phenix server writes them each time it saves a draft.
The editor never writes them. The Inspector shows them under **Details**
(see [With nothing selected](editor.md#with-nothing-selected)).

| Field | What it holds | Set |
|---|---|---|
| `createdBy` | The user who made the diagram | When a draft is created: the `createdBy` the document already names, else the user who creates the draft. Every later save keeps it. |
| `createdAt` | When the diagram was made | With `createdBy`, the same way |
| `updatedBy` | The user whose save stored this content | On every save: the user who saves |
| `updatedAt` | When that save was | On every save: the time of the save |

The times are in UTC, to the second, in the form `2026-09-29T18:17:59Z`. A
user name is at most 256 bytes. All four fields are optional, so a document
written by hand can have none. When such a diagram opens read only from a
file, it shows no **Details**.

What this means for each way of making a draft:

- **Blank diagram**, **Import**, and a legacy diagram converted with
  **Upload**: you made the diagram, and it is created now.
- **Upload**: the `createdBy` and `createdAt` the file names are kept. When
  the file names none, you made the diagram.
- **Edit as a draft** on a published diagram or on a diagram read from a
  file: the draft starts as that document, unchanged, so all four fields
  come from the document. After your first edit, you are the one who saved
  it last.
- A draft shared with **Can edit**: `createdBy` stays, and a save by the
  other person names that person in `updatedBy`.
- **Undo**, **Redo** and **Restore** go back to an earlier snapshot, which
  holds the `updatedBy` and `updatedAt` of the save that made it.

**Publish** stores the document as it is, with the four fields that it has.
These fields are part of the document, so they change its digest.

!!! note
    `createdBy` and `createdAt` are what the document says. A person who
    uploads a document can name anyone in it. The server writes `updatedBy`
    and `updatedAt` on each save that it makes. A document that phenix takes
    unchanged from a file keeps all four fields as the file has them. This
    applies to a diagram read from a Builder file, and to the draft that
    **Edit as a draft** makes from it, which can be published before its
    first edit. Those values are only as trustworthy as the people who can
    write the file.

`source.updatedAt`, lower in a document made by **Import**, is a different
time: when the imported config was last changed.

### Topology YAML

**Topology YAML** is the phenix Topology config that **Publish** would write
for the diagram. The phenix server makes it the way **Publish** does, and
checks it the same way. Nothing is written on the server.

The Riverside Water diagram downloads as this Topology. The file has ten
nodes. Only the first is shown:

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
- A device's notes are its node's `general.notes`, as publishing writes
  them. Note nodes on the canvas, a switch's notes, groups, drawings
  (rectangles, circles, icons and lines), colors, line styles, icons and
  icon sizes, device templates and positions are not in the file.

To store the file in phenix as a Topology config named `Riverside-Water`:

```bash
phenix config create riverside-water.topology.yaml
```

A topology stored this way is a plain Topology config. Builder did not
publish it, so if you edit it from **Configs**, Builder does not open. To
store the topology with its diagram, publish the draft (see
[Publishing](publishing.md)), or name the Builder file in the config (see
[A Builder file beside a topology](#a-builder-file-beside-a-topology)).

When the diagram cannot be published yet, the dialog still saves the file,
and lists every reason. The Riverside Water expansion draft gives:
"Saved riverside-water-expansion.topology.yaml. This topology cannot be
published yet: interface "eth0" of device "historian-01-2" has no VLAN:
connect it to a network, or type a VLAN for it."

![The Download diagram dialog of Riverside Water expansion after Topology YAML: riverside-water-expansion.topology.yaml was saved, and the topology cannot be published yet because interface eth0 of device historian-01-2 has no VLAN.](../images/builder/download-topology-blockers.png)

**Topology YAML** is a quick way to see every problem that publishing
would report (see
[What blocks publishing](publishing.md#what-blocks-publishing)). When phenix
cannot accept the config at all, the dialog shows the error and saves no
file.

Topology YAML needs the `configs` `get` permission.

### PNG and SVG

**PNG** and **SVG** draw the whole diagram, not only the part in view. The
picture covers the diagram bounds that the dialog shows. Its longer side is
at most 4096 pixels, for a diagram of any size. The Riverside Water PNG is
4096 × 1801 pixels. On a high-resolution screen, a PNG gets the extra pixels
of the screen, but stays within 4096 pixels in each direction.

The picture shows colors, line styles, group borders, custom icons and the
drawings (rectangles, circles, icons, and lines with their arrowheads) as
the canvas draws them. It does not show the items that are only for editing.
These are the selection, the connection points, the handles that resize a
node or move the points of a line, the warning marks and the info tooltips.
Its background follows the theme: white in the light theme, dark in the dark
theme (see [Themes](editor.md#themes)).

The SVG holds the diagram as HTML inside the SVG, and each custom icon as a
PNG inside it. Web browsers show it. Some drawing programs cannot.

### Gephi (GEXF)

**Gephi (GEXF)** saves the network as a graph for
[Gephi](https://gephi.org/) and other tools that read GEXF 1.3. Builder
cannot open this file.

The graph has:

- A node for each device, and one for each network. All the switches of a
  network are one node.
- An edge for each connection, from the device to its network. The edge
  label is the interface and its address, for example "eth0
  10.10.30.20/24".
- The colors and positions of the diagram. A device has its fill color, else
  its outline color, else the color of its type. A network has its
  **Edge Color**. Colors written in hex or `rgb()` are kept. A color written
  as a name, such as `red`, is not kept.

Notes, groups and drawings (rectangles, circles, icons and lines) are not
nodes. A node's groups are in its **Group** and **Groups** columns instead.

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

The **Scenario apps** columns list the apps of the diagram's scenarios that
run on each device. For example, historian-01 has `ntp`. Download first
reads from the server each scenario that the diagram lists. When it cannot
read one, the file has no app columns, and the dialog gives the reason, for
example "It lists no scenario
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

## Moving a diagram with a Builder package

A Builder document names the scenarios, included topologies, custom icons
and disk images of its diagram, but another phenix server may not have
them. A **Builder package** is one file, JSON or YAML. It holds the Builder
document, the configs and icons that you choose to put in, and the list of
everything that the diagram needs. When you upload it on another server, it
shows which of those items that server has. It creates a missing Scenario
or Topology config only when you tick it.

A package never holds the content of a file, a script or an app. It lists
disk images, apps, and files such as injection sources only by name.

### Downloading a package

1. Open the draft and select **Download**.
2. Under **Builder package**, tick what the file also carries:
    - **Scenario configs**: the Scenario configs the diagram names.
    - **Included topologies**: the Topology configs the diagram includes.
      An include that names a file is listed, not read.
    - **Custom icons**: a copy of each custom icon the diagram names, from
      the server's icon library.
    - **Disk-image requirements**: the disk images the devices boot from,
      each with the devices that use it.

    None is ticked at first. Whatever you tick, the file lists the
    scenarios, included topologies, templates and custom icons of the
    diagram, the apps of its scenarios, and the files that its devices and
    the configs in the package name.

3. Choose JSON or YAML in **Package format**, then select **Builder
   package**. The file is named after the diagram, for example
   `pump-station.package.json`.

The server reads each config with your permissions. The package lists, but
does not carry, a config that does not exist or that your role cannot read.
The dialog lists such a config before it saves the file, for example "Scenario config
pump-station-ntp does not exist on this server, or your role cannot read
it: the package names it but does not carry it." Select **Save package** to
save the file anyway, or **Do not save**. A config is carried without its
creation and update times and without the Builder's own annotations
(`builder-*`).

The list of requirements holds at most 1000 entries of each kind. Each
entry is at most 4096 bytes long and has no control characters. An entry
that does not obey these limits, such as an injection source with a line
break in it, is left out of the list. The dialog names it in the same way
before it saves the file, for example "The package does not list file "/phenix/injects/a\nb":
it must not contain control characters." Each warning shows its
[error code](error-codes.md), such as `package.config.unreadable`.

[pump-station.package.yaml](examples/pump-station.package.yaml) is the
pump station with **Scenario configs** and **Disk-image requirements**
ticked. It ends with its list of requirements:

```yaml
requirements:
  scenarios:
    - pump-station-ntp
  topologies: []
  templates: []
  icons: []
  images:
    - name: bennu.qc2
      usedBy:
        - rtu-01
    - name: minirouter.qc2
      usedBy:
        - station-rtr
    - name: windows10.qc2
      usedBy:
        - eng-ws-01
  apps:
    - ntp
  files: []
```

### Uploading a package

1. Select **Upload** and choose the package file, as for a Builder document
   (see [Uploading a Builder document](#uploading-a-builder-document)), then
   select **Upload**.
2. The dialog lists what the diagram needs, under **Scenario configs**,
   **Included topologies**, **Templates**, **Custom icons**, **Disk
   images**, **Apps** and **Files**. Each entry gives its status in words,
   and "in the package" when the file carries it:
    - **Present**: the server has it. A config or icon of that name with the
      same content as the copy in the package is present.
    - **Missing**: the server does not have it.
    - **Different**: the server has a config or icon of that name with other
      content. It is not changed.
    - **Not checked**: the server cannot check it, for example because your
      role cannot read that config, or cannot list disk images or apps.
      Disk images are also not checked when the server lists none, as it
      does when minimega is not running. Files on the server are never
      checked.

    Builder looks for a disk image or an app among the images or apps that
    your role can list. An image or an app that your role cannot see shows
    Missing. A drive whose image has the same file name as an image of the
    server, but another path, counts as present. The entry says which image
    matched, for example "Matched by file name ubuntu.qc2; this server's image is
    /phenix/images/ubuntu.qc2."

3. A Scenario or Topology config that is missing and in the package has a
   **Create on this server** checkbox, which is not ticked. Tick each config
   to create. No other entry has this checkbox. Builder never replaces a
   config that the server has, and it only lists disk images, apps and
   files.

4. Select **Continue to editor**. Builder does these steps:

    - It creates the ticked configs, one at a time. Each needs the
      `configs` `create` permission.
    - It adds the custom icons that the server does not have to its icon
      library, as an upload of a Builder document does.
    - It opens the diagram as a new draft.

    The diagram still names its scenarios and topologies as the file does,
    whether or not you created them.

The dialog in step 2, for
[pump-station.package.yaml](examples/pump-station.package.yaml) on a server
that has no Scenario config pump-station-ntp and none of the diagram's disk
images:

![The Upload diagram dialog after the upload of pump-station.package.yaml: the diagram needs 5 items, 4 of them missing on this server; under Scenario configs, pump-station-ntp is Missing, in the package, with an unticked Create on this server checkbox; under Disk images, bennu.qc2, minirouter.qc2 and windows10.qc2 are Missing, each with the device that uses it; under Apps, ntp is Present; and the Cancel and Continue to editor buttons.](../images/builder/package-upload.png)

While Builder creates the configs and adds the icons, the dialog stays open
and says what it is doing. **Cancel** and closing the dialog do nothing
until it is done. When a config cannot be created, or an icon cannot be
added, the dialog gives the reason before it opens the diagram. Builder
still creates the other configs. The dialog first names the configs that it
created, for example "Created Scenario config pump-station-ntp on this
server. This upload has 1 warning." **Cancel** then opens nothing. The
configs that Builder created stay, and Builder says so again when the
dialog closes.

The server refuses a package whose configs carry Builder annotations
(`builder-*`).

The phenix server describes the format as a JSON Schema at
`/api/v1/schemas/builder/package/v1`.

## Builder documents and phenix config create

A Builder document is not a config. `phenix config create` refuses one named
on the command line, and skips one it finds in a directory:

```console
$ phenix config create pump-station.builder.json
Error: pump-station.builder.json is a Builder document, not a configuration: upload it in the Builder, or send it to the Builder REST API (/api/v1/builder/drafts), and publish it to create its topology
$ phenix config create examples
2026-10-01 21:51:05.923 INF configuration created type=SYSTEM kind=Topology name=corp-services
2026-10-01 21:51:05.962 INF configuration created type=SYSTEM kind=Topology name=metro-campus
2026-10-01 21:51:05.962 INF skipped Builder document; upload it in the Builder to publish it type=SYSTEM path=examples/pump-station.builder.json
2026-10-01 21:51:05.994 INF configuration created type=SYSTEM kind=Topology name=pump-station
2026-10-01 21:51:05.994 INF skipped Builder document; upload it in the Builder to publish it type=SYSTEM path=examples/riverside-water.builder.json
2026-10-01 21:51:06.023 INF configuration created type=SYSTEM kind=Scenario name=riverside-water
2026-10-01 21:51:06.057 INF configuration created type=SYSTEM kind=Topology name=riverside-water
2026-10-01 21:51:06.086 INF configuration created type=SYSTEM kind=Role name=topology-designer
2026-10-01 21:51:06.115 INF configuration created type=SYSTEM kind=Role name=topology-reviewer
```

Here `examples` is a directory with the example configs and the two Builder
documents of these pages (see
[Load the example configs](index.md#load-the-example-configs)). A template
file or a Builder package, such as `node-templates.yaml` or
`pump-station.package.yaml`, is also not a config. `phenix config create`
skips one that it finds in a directory, with a debug log line that names
it. It refuses one named on the command line:

```console
$ phenix config create node-templates.yaml
Error: node-templates.yaml is a Builder template file, not a configuration: use Import templates in the Builder, or the Builder REST API (POST /api/v1/builder/templates/{owner}/items), to add its Node Templates
$ phenix config create pump-station.package.yaml
Error: pump-station.package.yaml is a Builder package, not a configuration: upload it in the Builder to open its diagram
```

To publish a document, upload it (see
[Uploading a Builder document](#uploading-a-builder-document)), then publish
the draft (see [Publishing](publishing.md)). To add the templates of a
template file, see [Importing](templates.md#importing). To open a package,
see [Uploading a package](#uploading-a-package). To do these steps from a
script, use the Builder REST API (see
[REST API](administration.md#rest-api)).

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
topology with the tag **File**. Its diagram opens from the file, without a
publish first. The file must be on the phenix server, below
`/phenix`. See
[Builder documents in files](administration.md#builder-documents-in-files)
for the example in full, the rules, and what **Publish** does with such a
topology.
