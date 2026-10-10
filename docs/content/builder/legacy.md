# Legacy Builder

Earlier phenix releases had another graphical editor, also named Builder,
built on mxGraph. It opened from the **Builder** tab in a new browser tab, at
`/builder`, and kept each diagram as XML in the `builder-xml` annotation of
the Topology config.

The legacy Builder was removed in
[sandialabs/sceptre-phenix#442](https://github.com/sandialabs/sceptre-phenix/pull/442).
The last commit that has it is
[`a0aeaa4e`](https://github.com/sandialabs/sceptre-phenix/commit/a0aeaa4ee899018196ca4a4aa6e9ce114cf66de4).
The [Builder](index.md) described in these pages replaces it, and converts
its diagrams.

## What changed

| | Legacy Builder | Builder |
|---|---|---|
| Where a diagram is kept | XML in the topology's `builder-xml` annotation | A draft on the phenix server. **Publish** stores the diagram and names it in the topology's `builder-doc` annotation |
| What writes the topology | **Save to phēnix** | **Publish** (see [Publishing](publishing.md)) |
| A line between two devices | A direct connection | A switch for each network. Converting adds the missing switch |
| Experiment variables | Placeholders such as `$DEFAULT_MEMORY` | The values, written into each node |
| Tag on the **Configs** page | `builder legacy` | `builder` |
| REST routes | `POST /builder/save`, `/api/v1/builder/topologies`, `/api/v1/experiments/builder` | Removed. See [REST API](administration.md#rest-api) |

Topologies made with the legacy Builder still run as they are. phenix never
read the diagram to run an experiment. Convert a diagram only when you want
to edit it as a diagram.

On the **Configs** page, a topology the legacy Builder saved has the tag
`builder legacy`. The viewer shows its annotation as
`builder-xml: <SNIPPED>`. **Edit** opens the topology as text, with the same
`<SNIPPED>` line. phenix saves the diagram back unchanged while that line
stays as it is. Deleting the line removes the diagram.

## Converting a stored topology

**Import** converts the diagram of a stored topology that has `builder-xml`
and no `builder-doc`:

1. Select **Builder** in the phenix navigation bar, then **Import**.
2. Keep **Stored config** and **Source kind** **Topology**. In
   **Source name**, a topology with a legacy diagram reads
   "NAME (legacy Builder diagram)". Choose it. The hint under the list says
   "This topology has a legacy Builder diagram. Its layout is converted.
   Publishing the draft to this topology replaces the legacy diagram."
3. Select **Import**. When the conversion has warnings, the dialog lists
   them, for example "This import has 3 warnings." (see
   [Warnings](#warnings)). Select **Continue to editor**.
4. Edit the draft.
5. Select **Publish**. Enter the topology's name in **Topology name**. The
   hint says "A topology with this name exists and will be updated. Its
   legacy Builder diagram is replaced by this diagram."
6. Select **Update topology**, and **Update topology** again to confirm.
   The result lists the warning "The legacy Builder diagram of topology
   NAME was replaced by this diagram."

The topology's own nodes are used. The old diagram only says where each node
sits, matched by hostname, and adds its notes and groups.

Nothing changes on the server until you publish. Publishing to the same
topology removes `builder-xml`, writes `builder-doc` and keeps the other
annotations. The topology then has the tag `builder` on the **Configs** page,
like any topology published from the Builder.

On the **Configs** page, the `builder legacy` tag and the viewer's
**Import into Builder** button open the same **Import** dialog, set to that
topology. The dialog says "Topology NAME has a legacy Builder diagram.
Import it to convert the diagram." (see
[Import, Upload and Download](import-upload-download.md#from-the-configs-page)).

The import options work for a legacy topology too (see
[Import options](import-upload-download.md#import-options)). A copy, or a
draft that combines the included topologies, publishes a new topology. The
legacy topology keeps its `builder-xml`.

Only the draft imported from the topology itself can replace its legacy
diagram. A draft imported from an experiment made from the topology, an
uploaded file, and any other draft get the hint "A topology with this name
already exists, and this diagram cannot update it: …". Publish them under
another name.

The topology, its diagram included, must still be what the draft was
imported from. When it changed since, Publish refuses with "Could not
publish the diagram. Builder source Topology/NAME changed after this draft
was imported." Import the topology again.

When the import cannot read the diagram, it warns "The legacy diagram of
topology NAME could not be read (…), so its layout was not used. Nodes were
placed automatically." The Publish hint then ends "Its legacy Builder diagram
could not be read and is removed.", and the result warns "The legacy Builder
diagram of topology NAME could not be read and was removed." Publishing
removes `builder-xml` in this case too.

## Converting a file

**Upload** converts a legacy diagram in a file:

1. Select **Upload**, on the drafts page or in the editor toolbar.
2. Under **Source**, choose **Legacy Builder diagram or Topology**.
3. In **Legacy diagram or Topology file**, choose the file.
4. Select **Convert**. When the conversion has warnings, the dialog lists
   them ("This conversion has 3 warnings."). Select
   **Continue to editor**. The editor opens a new draft, named after the
   file without its last extension.

![The Upload diagram dialog with the source Legacy Builder diagram or Topology chosen and sample.xml chosen as the Legacy diagram or Topology file; its hint says the file is a diagram saved by the legacy Builder (XML) or a Topology config that has the builder-xml annotation (YAML or JSON), up to 5 MiB, converted into a new draft; with the Cancel and Convert buttons.](../images/builder/upload-legacy.png)

The file can be:

- an XML file that the legacy Builder saved with **Save to Disk**, whose root
  element is `mxGraphModel` (or a bare `root`), or
- a Topology config, as YAML or JSON, that has the `builder-xml` annotation,
  such as one downloaded from the **Configs** page.

The file can be up to 5 MiB. The converter does not read draw.io files
(`mxfile`), compressed or base64-encoded diagrams, and there is no field to
paste a diagram into. A legacy diagram given to the **File** or
**Paste text** source is not converted: the dialog says "This looks like a
legacy Builder diagram (XML). Choose "Legacy Builder diagram or Topology" to
convert it."

A file that is neither is refused, for example: "Could not convert the
legacy diagram. This is not a legacy Builder diagram: expected mxGraph XML,
or a Topology config with the builder-xml annotation." A Topology config
without the annotation gives "Could not convert the legacy diagram. Topology
NAME has no legacy Builder diagram (no builder-xml annotation); use Import
to make a diagram from it."

A diagram without its topology has only the node settings the diagram itself
holds. A draft converted from a file always publishes a new topology: it
cannot update a stored topology, even one with the same name.

Converting a file needs the `configs` `get` and `configs` `create`
permissions. phenix reads a Topology config file as it reads any config
file, so `${NAME}` in it is filled in from the phenix server's environment
(see [Importing a config file](import-upload-download.md#importing-a-config-file)).
Closing the dialog while it converts cancels the conversion.

## What is converted

| In the legacy diagram | In the Builder |
|---|---|
| A device | A device. For a topology, its settings come from the topology; for a diagram without one, from the diagram |
| A router, firewall, desktop or server icon, in any color | The `router`, `firewall`, `desktop` or `server` icon. An external device gets `external`; other icons keep the icon of the device's type |
| A switch | The switch of its network, named after the network |
| A VLAN ID on a switch or a line | The network's **VLAN alias**, when it is a number from 1 to 4094. `0`, `auto` or none give no alias |
| A line from a device to a switch | A connection |
| A line between two devices | A connection to a switch added for that network, placed between its devices |
| Text | A note |
| A rectangle or container around nodes | A group, titled with its text |
| A placeholder such as `$DEFAULT_VCPU` | Its value: the diagram's own variable, else the legacy default (`DEFAULT_MEMORY` 2048, `DEFAULT_VCPU` 1, `DEFAULT_VM_IMAGE` `ubuntu.qc2`, `DEFAULT_ROUTER_IMAGE` `vyos.qc2`). The value stays text, such as `vcpus: "1"`, as the legacy Builder wrote it. Only for a diagram without a topology |
| Positions | Doubled, and put on the 16-pixel grid, with each device centered where its icon was |
| The grid setting | Kept |

Positions are doubled because a node card is about twice the size of the old
icon. A converted diagram is a diagram like any other: each device shows its
type, such as `Router` or `External`, and a network's VLAN ID shows as the
VLAN alias of its switch.

For a topology, a node that is in the diagram but not in the topology is left
out, and a node of the topology that is not in the diagram is placed below
it. Each gives a warning.

### What is left out

- The legacy switch's own name: a switch is named after its network.
- The diagram's variables, once they are filled in, and the `schema` and
  `device` keys of each node.
- Icon colors and artwork, and the `mobile` icon.
- Shapes, fills, strokes, fonts, rotation, shadows, dashes and arrows, and
  the HTML formatting of labels.
- Edge labels, waypoints and lines that are not links between a device and a
  network.
- Layers (merged into one), whether a cell was hidden, collapsed or locked,
  and the stacking order.
- Tooltips, links and inserted images.
- Shapes without text.
- Page settings, such as the page size, background and grid size.
- A group nested more than 32 deep leaves its parent group.

## Warnings

The dialog lists every warning before it makes the draft. Lists of names
show at most 8 names, then how many more.

| Warning | What it means |
|---|---|
| "The legacy diagram of topology NAME could not be read (…), so its layout was not used. Nodes were placed automatically." | The topology's `builder-xml` is not a diagram the converter reads. The draft has the topology's nodes, laid out on a grid. Publishing removes the old diagram |
| "1 node of the legacy diagram is not in the topology and was left out: …" | The topology was changed after its diagram was last saved. The topology's nodes are used |
| "1 node of the topology was not in the legacy diagram and was placed below it: …" | The same, the other way round |
| "Added a switch for 1 network that had none in the legacy diagram: …" | The diagram joined two devices with a line, or did not draw the network |
| "Left out 1 link whose node has no interface on that network: NODE to NETWORK." | A line joins a device to a network that none of its interfaces uses |
| "VLAN ID VALUE of network NAME is not a number from 1 to 4094 and was left out." | The network has no VLAN alias |
| "Network NAME has more than one VLAN ID in the legacy diagram. The first, ID, was kept." | Its switches or lines gave other IDs |
| "VLAN ID ID is used by networks A and B. It was kept for A only." | Two networks cannot share an alias |
| "The legacy diagram had N layers. They were merged into one." | Every node is on the one canvas |
| "1 item has settings that could not be read and was left out: …" | A node's settings in the diagram are damaged |
| "1 node has interfaces that could not be read and were left out: …" | A diagram without a topology only. The node is kept without those interfaces |
| "Renamed node "OLD" to "NEW": a hostname cannot hold spaces." | Spaces became hyphens. The same for a network: "a network name cannot hold spaces" |
| "Used the legacy defaults for variables the diagram does not define: $DEFAULT_MEMORY = 2048, …" | Check the values in the Inspector |
| "1 placeholder has no value in the diagram and was kept as text: $NAME." | Replace the text in the Inspector before you publish |
| "Left out 1 line that was not a network link." | See [What is left out](#what-is-left-out) |
| "Left out 1 shape that had no text." | The same |
| "Text and containers were kept as notes and groups. Their colors, fonts and other formatting were not converted." | Given once, when the diagram has text or containers |
| "The diagram has no nodes." | The draft is empty |

The import also gives the warnings any import gives, for example for
included topologies.

## Keeping a copy of the old diagram

Download the topology from the **Configs** page before you publish: the file
still holds the XML, and **Upload** converts it again. To open the old
editor itself, use a phenix build at or before `a0aeaa4e`.
