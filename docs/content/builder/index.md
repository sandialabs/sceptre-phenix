# Builder

Builder is the phenix web editor for drawing topologies as diagrams. You
place devices and switches on a canvas, connect them, and fill in each
device's settings in a form. Your work is saved as a draft on the phenix
server, apart from the phenix configs. Only **Publish** writes Topology and
Experiment configs and adds a topology to its scenarios, and only the
**Scenarios** dialog stores a Scenario config, from a file you upload.

![The Builder editor showing the Riverside Water diagram: the header and toolbar at the top, Add nodes and Outline on the left, the canvas with the DMZ, INTERNET, OT and CORP groups in the middle, and the Inspector with the diagram's details, annotations and scenario on the right.](../images/builder/overview-editor.png)

## What you can do with Builder

- Draw a topology: devices, switches (one per network), the connections
  between them, notes, groups, and drawings (rectangles, circles, icons and
  lines). Lay it out with one of four automatic layouts. See
  [Building a Diagram](diagrams.md).
- Start from a Topology or Experiment config, stored in phenix or in a
  config file, with **Import**, or from a file you have, such as a Builder
  document, with **Upload**. **Download** saves a diagram as a file. See
  [Import, Upload and Download](import-upload-download.md).
- Open a topology in the Builder from the **Configs** page. See
  [From the Configs page](import-upload-download.md#from-the-configs-page).
- Give nodes colors, line styles and your own icons, and keep device
  templates in the diagram or in your own **Node Templates** library, which
  you can share. See [Building a Diagram](diagrams.md) and
  [Node Templates](templates.md).
- Keep your work safe: each edit is saved to the draft on the server, and
  **Draft History** lists the earlier versions to restore. See
  [Drafts](drafts.md#how-drafts-save).
- Share a draft with other users, who can view it or edit it. See
  [Sharing a draft](drafts.md#sharing-a-draft).
- Check the diagram for problems before you publish, such as an interface
  with no network or two interfaces with the same address. See
  [Checks and warnings](editor.md#checks-and-warnings).
- Publish a Topology config, or a Topology and an Experiment config with one
  of the diagram's scenarios, from the editor or from the drafts page. See
  [Publishing](publishing.md).
- Download the diagram as Builder JSON or YAML, as the Topology YAML that
  Publish would write, as a PNG or SVG picture, or as a Gephi (GEXF) graph.
  See [Downloading](import-upload-download.md#downloading).
- Make a topology from a Builder JSON or YAML file on the command line,
  with `phenix builder publish`. See
  [From the command line](import-upload-download.md#from-the-command-line).
- Keep a diagram as a file next to its topology, for example in a
  repository checked out on the phenix server. See
  [Builder documents in files](administration.md#builder-documents-in-files).
- See who made a diagram and who edited it last. See
  [With nothing selected](editor.md#with-nothing-selected).
- Work from the keyboard, with a command palette and shortcuts. See
  [Command palette](editor.md#command-palette) and
  [Keyboard shortcuts](editor.md#keyboard-shortcuts).

## The legacy Builder

Earlier phenix releases had a different graphical editor with the same
name. It was removed. Topologies it saved keep their `builder-xml`
annotation and have the tag `builder legacy` on the **Configs** page.
**Import** converts such a topology into a draft, and **Upload** converts a
diagram file the legacy Builder saved. See [Legacy Builder](legacy.md).

## The example lab

The examples on these pages use one small lab, **Riverside Water**: a water
utility with an internet edge, a DMZ, a corporate network, and an OT
(control) network behind a firewall. It is the Topology config
`riverside-water`. Two of its servers, `dns-01` and `ntp-01`, come from a
second topology, `corp-services`, which `riverside-water` includes. The
Scenario config `riverside-water` configures NAT on the edge router (the
`vrouter` app) and time service from `ntp-01` (the `ntp` app).

| Network (VLAN) | Subnet | Devices and their addresses |
|---|---|---|
| `INTERNET` | 203.0.113.0/24 | `kali-01` .50, `edge-rtr` .2 (gateway .1) |
| `DMZ` | 172.16.10.0/24 | `edge-rtr` .1, `web-01` .10 |
| `CORP` | 10.10.20.0/24 | `edge-rtr` .1, `files-01` .10, `ws-01` .101, `ws-02` .102, `ot-fw` .254, and from `corp-services`: `dns-01` .53, `ntp-01` .123 |
| `OT` | 10.10.30.0/24 | `ot-fw` .1, `hmi-01` .10, `historian-01` .20, `plc-01` .50 |

| Device | What it is |
|---|---|
| `edge-rtr` | VyOS edge router (type Router, image `vyos.qc2`), with a route to 10.10.30.0/24 through `ot-fw` |
| `ot-fw` | Firewall between CORP and OT (type Firewall, image `minirouter.qc2`); its ruleset `corp-to-ot` lets only HTTPS from CORP to `historian-01` through |
| `kali-01` | Red team host on the internet (`kali.qc2`) |
| `web-01` | Public web server (`ubuntu.qc2`) |
| `files-01` | Corporate file server (`ubuntu.qc2`) |
| `ws-01`, `ws-02` | Engineering and office workstations (`windows10.qc2`) |
| `hmi-01` | Operator HMI (`windows10.qc2`) |
| `historian-01` | Process historian (`ubuntu.qc2`) |
| `plc-01` | Pump station PLC, an external (hardware in the loop) device |
| `dns-01`, `ntp-01` | DNS and time servers, from `corp-services` (`ubuntu.qc2`) |

Two more topologies appear on some pages: `metro-campus`, a larger topology
of 42 devices on 7 networks, for trying the layouts, and `pump-station`, a
file that you import without storing it in phenix first.

### Load the example configs

Download the example files:

- [corp-services.topology.yaml](examples/corp-services.topology.yaml)
- [riverside-water.topology.yaml](examples/riverside-water.topology.yaml)
- [riverside-water.scenario.yaml](examples/riverside-water.scenario.yaml)
- [metro-campus.topology.yaml](examples/metro-campus.topology.yaml)
- [pump-station.topology.yaml](examples/pump-station.topology.yaml) (not
  stored; used in [Import, Upload and Download](import-upload-download.md))
- [pump-station.builder.json](examples/pump-station.builder.json) (a Builder
  document; used in [Import, Upload and Download](import-upload-download.md))
- [riverside-water.builder.json](examples/riverside-water.builder.json) (a
  Builder document; see [The drafts on these pages](#the-drafts-on-these-pages))

Then store the first four configs in phenix and create the experiment
`riverside` from them.

#### From the Command Line Binary

```bash
phenix config create corp-services.topology.yaml riverside-water.topology.yaml riverside-water.scenario.yaml metro-campus.topology.yaml
phenix experiment create riverside -t riverside-water -s riverside-water
```

#### From the Web-UI

1. Select **Configs** in the navigation bar.
2. Select the upload button (its tooltip says **upload a new config**). In
   the **Upload a Config** dialog, drop `corp-services.topology.yaml`, or
   select the drop area and choose the file.
3. Upload `riverside-water.topology.yaml`, `riverside-water.scenario.yaml`
   and `metro-campus.topology.yaml` the same way.
4. Select **Experiments**, then **Create One Now!**. When the server already
   has experiments, select the **+** button (**Create a new experiment**)
   instead.
5. Enter **Experiment Name** `riverside`, choose **Experiment Topology**
   `riverside-water` and **Experiment Scenario** `riverside-water`, and select
   **Create Experiment**.

See [Create a Config](../configuration.md#create-a-config) and
[Create a New Experiment](../experiments.md#create-a-new-experiment) for
more about these pages.

### The drafts on these pages

Most pages show drafts made from the example lab:

- **Riverside Water**: the import of `riverside-water`, renamed, laid out with
  **ELK layered**, grouped with **Auto-group** > **By network**, with a note
  and the scenario `riverside-water` listed. To get the same draft, select
  **Upload** on the drafts page and upload `riverside-water.builder.json`
  (see [Uploading a Builder document](import-upload-download.md#uploading-a-builder-document)).
- **Riverside Water expansion**: a second copy of Riverside Water with a
  duplicate of `historian-01`, which gives the diagram three warnings. To
  make it:
    1. On the drafts page, select **Upload** and upload
       `riverside-water.builder.json` again. Upload always makes a new draft,
       so you now have two drafts named Riverside Water. The new one opens.
    2. Select **Edit diagram name** and rename the new draft
       `Riverside Water expansion` (see
       [Renaming the diagram](editor.md#renaming-the-diagram)).
    3. Select `historian-01` and press <kbd>⌘</kbd>+<kbd>D</kbd> on macOS or
       <kbd>Ctrl</kbd>+<kbd>D</kbd> on Windows and Linux (see
       [Duplicating a device](diagrams.md#duplicating-a-device)). The copy is
       named `historian-01-2`, and the header says **3 warnings**.
- **Metro Campus**: the import of `metro-campus`, renamed Metro Campus (see
  [Renaming the diagram](editor.md#renaming-the-diagram)), before any layout.

A draft made from `riverside-water` can publish only while `riverside-water`
is unchanged, apart from that draft's own publications. So once one draft
publishes `riverside-water`, the drafts made from it before then can no
longer publish, and neither can a draft uploaded from
`riverside-water.builder.json` later. The [quick start](#quick-start)
publishes `riverside-water` from the quick start draft, and
[Publishing a topology](publishing.md#publishing-a-topology) publishes it from
Riverside Water. After either, Riverside Water expansion can no longer
publish (see
[When the source config changed](publishing.md#when-the-source-config-changed)).

## Quick start

This quick start imports the topology `riverside-water`, adds a workstation
to the CORP network, and publishes the topology. Load the example configs
first (see [The example lab](#the-example-lab)).

### Import the topology

1. Select **Builder** in the phenix navigation bar. The drafts page
   opens.
2. Select **Import**. In the **Import topology or experiment** dialog, keep
   **Stored config** and **Source kind** **Topology**. Choose
   `riverside-water` under **Source name**. Keep
   **Keep included nodes read only** under **Included topologies**, leave
   **Create a new topology as a copy** clear, and select **Import**.
3. The dialog says "This import has 1 warning." The warning says that 2
   nodes come from the included topology `corp-services` and are shown read
   only. These are `dns-01` and `ntp-01`. Select **Continue to editor**. A
   new draft named riverside-water opens, with its devices in rows on a
   grid.
4. Open the layout menu in the toolbar, which says **Default**, and choose
   **ELK layered**. Each network's switch now sits next to its devices.

The dialog in step 2:

![The Import topology or experiment dialog with Stored config, Source kind Topology and Source name riverside-water selected, Included topologies set to Keep included nodes read only, Create a new topology as a copy clear, and the Cancel and Import buttons.](../images/builder/quickstart-import-dialog.png)

### Add a workstation

1. Under **Device templates**, select **Workstation**. A device named
   workstation appears in free space on the canvas. It is selected, and the
   Inspector on the right shows its settings.
2. In the Inspector, set **Hostname** to `ws-03` and **Description** to
   `Engineering workstation`.
3. Under **Network**, select **Add interface**. The new interface's
   **Interface kind** is **DHCP or manual**. Change it to **Static or OSPF**,
   and select **Switch and clear** when Builder asks "Switch Interface
   kind to Static or OSPF?".
4. Enter **Name** `eth0`, **VLAN** `CORP`, **Address** `10.10.20.103`,
   **Mask** `24` and **Gateway** `10.10.20.1`.
5. Select **Apply**. Because you typed the VLAN `CORP`, Builder
   connects eth0 to the CORP switch, and the canvas shows the new
   connection.
6. Check that the header shows **No issues** and the toolbar shows **All
   changes saved**. The header counts now show 13 devices and 16
   connections.

The interface before step 5:

![The Inspector for ws-03 with interface eth0 set to VLAN CORP, address 10.10.20.103, mask 24 and gateway 10.10.20.1, and Unapplied changes with Apply and Cancel at the bottom.](../images/builder/quickstart-ws03.png){ width="354" }

### Publish the topology

1. Select **Publish** in the toolbar. In the **Publish diagram** dialog,
   keep **Topology only**. **Topology name** is `riverside-water`, and the
   dialog says "A topology with this name exists and will be updated."
   **Checks** says that 11 devices, 4 switches, 4 networks and 14
   connections are ready to publish: `dns-01` and `ntp-01` stay in
   `corp-services`, and the topology includes them by reference.
2. Select **Update topology**. Builder asks "Replace topology
   riverside-water?". Select **Update topology** again.
3. The dialog says "Published. Every stage succeeded." Select **Close**.

The confirmation in step 2, and the result in step 3:

![The confirmation Replace topology riverside-water?, saying publishing replaces the topology on the server and cannot be undone, with Cancel and Update topology.](../images/builder/quickstart-publish-confirm.png)

![The Publish diagram dialog after publishing: Published. Every stage succeeded, with the document created, topology updated and draft ok, and a Close button.](../images/builder/quickstart-publish-result.png)

The Topology config `riverside-water` now has `ws-03`. On the **Configs**
page it has the tag `builder`. The tag, its **Edit** button and
**Open in Builder** in its viewer open it in Builder, in the quick start
draft (see
[Editing a published topology](publishing.md#editing-a-published-topology)).
Experiments made from it before, such as `riverside`, keep the copy of the
topology they were created with. To run the new topology, create an
experiment from it (see
[Create a New Experiment](../experiments.md#create-a-new-experiment)), or
publish the diagram as a topology and an experiment (see
[Publishing a topology and an experiment](publishing.md#publishing-a-topology-and-an-experiment)).

## Where to go next

- [Drafts](drafts.md): the drafts page, saving, Draft History, sharing and
  published diagrams.
- [The Editor](editor.md): the header, toolbar, Outline, Inspector, checks,
  command palette, keyboard shortcuts and settings.
- [Building a Diagram](diagrams.md): adding and connecting devices, device
  settings, groups, notes, layouts and scenarios.
- [Node Templates](templates.md): device templates in a diagram, your
  template library, collections, sharing and server-wide templates.
- [Import, Upload and Download](import-upload-download.md): starting from a
  config or a Builder document, every download format, and the
  `phenix builder publish` command.
- [Publishing](publishing.md): writing Topology, Scenario and Experiment
  configs, and what blocks publishing.
- [Administration](administration.md): permissions and the Builder role,
  storage, the `builder-doc` annotation, Builder documents in files, the
  REST API and troubleshooting.
- [Legacy Builder](legacy.md): converting the diagrams of the editor that
  the Builder replaced.
