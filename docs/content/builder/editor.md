# The Editor

The editor is where you draw and change a diagram. It opens when you open a
draft from the drafts page (see [Opening a draft](drafts.md#opening-a-draft)).
This page describes each part of the editor, its keys and its settings. For
tasks, such as adding devices and connecting them, see
[Building a Diagram](diagrams.md).

The examples on this page use the Riverside Water draft of the
[example lab](index.md#the-example-lab).

![The Builder editor showing the Riverside Water diagram: the header and toolbar at the top, Nodes & Templates on the left, the canvas with the DMZ, INTERNET, OT and CORP groups in the middle, and the Inspector with the diagram's name, description, icon size, grid size and layers on the right.](../images/builder/overview-editor.png)

The editor has five areas:

1. The [header](#header), with the diagram name, its counts, the checks and
   the view buttons.
2. The [toolbar](#toolbar), with the editing, layout, download and publish
   buttons, and the save state.
3. The left column, **Nodes & Templates**, with
   [Nodes & Objects](#nodes-objects) and its templates.
4. The [canvas](#canvas), where the diagram is drawn.
5. The [Inspector](#inspector), where you change what is selected.

## Header

![The editor header: Drafts, the name Riverside Water with its Edit diagram name pencil, the counts 12 devices, 4 switches, 4 networks, 15 connections, 4 groups and 1 note, No issues, and the Reset view, Commands, theme, Shortcuts with its key ?, Settings, Help and Focus mode buttons.](../images/builder/editor-header.png)

From left to right, the header holds:

- **Drafts**: returns to the drafts page. Your changes continue to
  save (see [Opening a draft](drafts.md#opening-a-draft)).
- The diagram name. Builder cuts a long name. Point at it to see all of it.
- **Edit diagram name** (the pencil): renames the diagram. It is not shown
  on a published diagram, which opens read only.
- The counts: devices, switches, networks, connections, groups and notes,
  each a number after an icon of its kind. While a device or a switch is at
  a layer, a layers count also shows, last. Each count is a button that
  selects every item of its kind on the canvas, in place of the selection (see
  [Selecting by kind](#selecting-by-kind)).
- The checks button: **No issues**, or the number of errors and warnings,
  for example **3 warnings**. It opens the **Diagram checks** dialog (see
  [Checks and warnings](#checks-and-warnings)).
- **Reset view**: resets the view, not the diagram (see [Canvas](#canvas)).
- **Commands**: opens the [command palette](#command-palette).
- The theme button: switches the theme (see [Themes](#themes)).
- **Shortcuts**: opens the [keyboard shortcuts](#keyboard-shortcuts) sheet.
  The button shows the key that opens the sheet: <kbd>?</kbd>, or the key
  you gave it. With **Single-key shortcuts** off and no other key, it shows
  none.
- **Settings**: opens **Builder settings** (see [Settings](#settings)). Its
  tooltip shows its key: "Builder settings (⌥⇧S)" or "Builder settings
  (Alt+Shift+S)".
- **Help**: opens this Builder documentation in a new tab.
- **Focus mode**: hides the phenix navigation bar (see
  [Focus mode](#focus-mode)).

In a narrower window, the buttons after the checks button show only their
icons, but **Shortcuts** keeps its key. Point at a button, or move focus to
it, to see its name in a tooltip.

### Selecting by kind

Select a count to select every item of that kind on the canvas, in place of
the selection you had. Focus stays on the count. For example, select
**12 devices** in Riverside Water: the 12 devices are selected, and Builder
announces "Selected 12 devices."

- The networks count selects the switches of the networks, because a network
  is drawn as its switch. Its tooltip says "Select the switches of all 4
  networks".
- The connections count selects the connections and no nodes.
- The layers count shows only while a device or a switch is at a layer
  (see [Layers](diagrams.md#layers)). It counts the layers that the
  Inspector lists, the placeholder layers included. For example, Lakeside
  Water Treatment shows **7 layers**. The count selects every
  device and switch that is at a layer: the layer chosen for the node, or
  the layer that its fields match. Its tooltip says "Select all nodes on a
  layer". To select the nodes of one layer only, use **Select nodes on
  layer** in the [command palette](#command-palette).
- A count of 0 selects nothing, and says so, for example "There are no notes
  to select."

Point at a count, or move to it with <kbd>Tab</kbd>, to see what it
selects, for example "Select all 12 devices". The counts are one
<kbd>Tab</kbd> stop: the arrow keys, <kbd>Home</kbd> and <kbd>End</kbd>
move between them, and <kbd>Enter</kbd> or <kbd>Space</kbd> selects.

### Renaming the diagram

To rename the Riverside Water diagram to "Riverside Water lab":

1. Select **Edit diagram name** (the pencil after the name). A field
   replaces the name, with the name selected.
2. Type `Riverside Water lab`.
3. Press <kbd>Enter</kbd>. The header shows the new name.

Press <kbd>Esc</kbd> instead to keep the old name. Leaving the field also
renames the diagram. **Undo** puts the old name back.

The **Name** field of the [Inspector](#with-nothing-selected) changes the
same name. The diagram name is not the name of the topology: you choose
that when you publish (see [Publishing](publishing.md)).

## Toolbar

![The editor toolbar: Undo, Redo, Copy, Paste, Delete, Group, Ungroup, Auto-group, the layout menu set to ELK layered, Scenarios, Download, Upload, Publish, Minimap, Draft History and the save state All changes saved.](../images/builder/editor-toolbar.png)

The toolbar's buttons, from left to right:

| Button | What it does | More |
|---|---|---|
| **Undo**, **Redo** | Undo or redo the last change. | [Undo and redo](diagrams.md#undo-and-redo) |
| **Copy**, **Paste** | Copy the selection, and paste a copy of it. | [Copy, paste, duplicate and delete](diagrams.md#copy-paste-duplicate-and-delete) |
| **Delete** | Delete the selected nodes and connections. | [Copy, paste, duplicate and delete](diagrams.md#copy-paste-duplicate-and-delete) |
| **Group**, **Ungroup** | Put the selected nodes in a new group, or remove a selected group and keep its nodes. | [Groups](diagrams.md#groups) |
| **Auto-group** | A menu: **By network**, **By name** or **By name pattern…**. | [Auto-group](diagrams.md#auto-group) |
| Layout menu | Arranges the diagram. It shows the draft's layout, or **Default** when no layout ran. | [Layouts](diagrams.md#layouts) |
| **Scenarios** | Choose the scenarios the diagram is used with. You can also view or edit a scenario, create a new one, or store a scenario file on the server. | [Scenarios](diagrams.md#scenarios) |
| **Download** | Save the diagram as a file. | [Downloading](import-upload-download.md#downloading) |
| **Upload** | Open a diagram you have as a new draft: a Builder document, a published diagram, or a legacy Builder diagram. | [Import, Upload and Download](import-upload-download.md) |
| **Publish** | Write Topology and Experiment configs, and add the topology to the diagram's scenarios. | [Publishing](publishing.md) |
| **Exp** | Open the experiment the diagram was published with. Shown only while that experiment exists and your role can read it. | [Opening the experiment](drafts.md#opening-the-experiment) |
| **Minimap** | Show or hide the minimap. | [Canvas](#canvas) |
| **Draft History** | List the draft's snapshots, and restore one. | [Draft History](drafts.md#draft-history) |

After **Draft History** comes the save state, for example "All changes
saved". A **Retry saving** button follows it when a save fails. See
[How drafts save](drafts.md#how-drafts-save).

A button that does not apply now is unavailable. For example, **Ungroup**
is unavailable until you select a group. On a published diagram, the
editing buttons are unavailable.

Point at a button to see its keyboard shortcut in a tooltip, for example
"Undo (⌘Z)" on macOS and "Undo (Ctrl+Z)" on Windows and Linux. The tooltips
of the two menus name the keys that run them without opening them. The
layout menu's tooltip says "Change layout (⌥⇧L)". These keys run **Auto
layout**. The **Auto-group** tooltip says "Group the ungrouped nodes
automatically. ⌥⇧G groups by network".

**Add connection** and **Move to group** are not in the toolbar. They are
in the header of the [Inspector](#the-inspector-header), and in the
command palette.

## Canvas

The canvas shows the diagram: devices, switches, notes, groups, drawings
(see [Shapes, icons and lines](diagrams.md#shapes-icons-and-lines)), and the
connections between devices and switches.

- **Select** a node or connection: click it. Shift-click adds a node to the
  selection or removes it from the selection. Click an empty spot to clear
  the selection.
- **Move** a node: drag it. The node follows the pointer from the moment
  you press, and goes where you drop it. With **Snap to grid** on in the
  [Settings](#settings), a drag puts nodes on
  the diagram's grid, which is 16 pixels by default (see **Grid size** in
  [With nothing selected](#with-nothing-selected)). Dragging a group moves
  the nodes in it. Shift and an arrow key move the selected nodes 10
  pixels and do not snap them to the grid (see
  [Arranging by hand](diagrams.md#arranging-by-hand)).
- A press is a click while the pointer stays within 4 screen pixels of
  the point you pressed. This is true at any zoom. With a finger or a pen,
  the limit is 8 pixels. A click selects the node and does not move it.
  This is true with **Snap to grid** on or off.
- When Builder cannot take a move, for example while a save conflict is
  open, the nodes return to their old places.
- **Pan**: drag an empty spot of the canvas.
- **Zoom**: use the mouse wheel, or the zoom controls at the bottom left:
  **Zoom in**, **Zoom out** and **Fit diagram to view**. After a fit, the
  third button is named **Restore previous view** and puts back the zoom and
  position from before the fit.
- **Connect**: drag from a device's connection point to a switch (see
  [Connecting interfaces](diagrams.md#connecting-interfaces)).
- **Edit** a node in a large dialog: double-click it (see
  [Node Editor](#node-editor)). A double-click on a line adds a bend.

A device shows its icon, its hostname, its node type, its number of
interfaces and its description. The node type is the device's **Type** as
stored, for example VirtualMachine, Router or Firewall. An external
device shows External, and a device with no type shows Device. A device from
an included topology has a dashed border. In place of its type, it shows
where it comes from, for example "Included from corp-services" on dns-01 and
ntp-01 (see [Included topologies](diagrams.md#included-topologies)). A node
with a warning has a warning mark at its top left corner (see
[Checks and warnings](#checks-and-warnings)).

A switch shows its icon and its name, which is the name of its network.
Its outline has the color of its network's connections, unless the switch
has an outline color of its own or the color of its layer. The icon of a
device or a switch has the color of its outline. A device with no outline
color has its icon in the accent color of the theme. With **Show node
icons** off in the [Settings](#settings), nodes show no icons.

Devices and switches can have colors and icons of their own (see
[Colors](diagrams.md#colors) and [Custom icons](diagrams.md#custom-icons)),
and notes, which show in a card below the node (see
[Notes on devices and switches](diagrams.md#notes-on-devices-and-switches)).

The minimap at the bottom right shows the whole diagram, with an outline
around the visible part. To resize it, drag the **Resize minimap** handle at
its top left corner. **Minimap** in the toolbar shows or hides it until the
next diagram opens. The Settings choose whether it shows at first (see
[Settings](#settings)).

With **Show coordinates** on in the Settings, rulers along the top and left
edges of the canvas show X and Y. The numbers are canvas pixels from the
origin of the canvas, as in **Position** in the Inspector. The rulers move
when you pan or zoom. A band on each ruler shows the extent of the selected
nodes. For a node in a group, **Position** starts at the group's top left
corner, so it does not agree with the rulers. The rulers are not in PNG and
SVG downloads.

![The top left part of the canvas with Show coordinates on: a ruler along the top edge with X from 0 to 500, and a ruler along the left edge with Y from 0 to 400. edge-rtr in the DMZ group is selected, and a blue band on each ruler shows the extent of edge-rtr.](../images/builder/canvas-coordinates.png){ width="600" }

The keys that work on the canvas are in
[Keys on the canvas](#keys-on-the-canvas).

**Reset view** in the header does these things, and does not change the
diagram:

- It sets the zoom back to how diagrams open (100%, the whole diagram, or
  your custom percentage, as the Settings say).
- It shows the minimap at its default size (unless the Settings turn it
  off).
- It shows both side columns at their default widths.
- It closes the sections you opened, such as **More settings**, and
  expands the sections and entries you collapsed in the Inspector and the
  Node Editor (see [Collapsing sections](#collapsing-sections)).
- It scrolls the columns back to the top.

### Info tooltips

Rest the pointer on a device or a switch for a moment, or move keyboard
focus to it, to see an info tooltip:

| Node | Rows of the tooltip |
|---|---|
| Device | **Description**, cut to 80 characters. **Interfaces**: each interface with its address, for example "eth0 — 10.10.20.101/24", or "DHCP". **OS type**. |
| Switch | **Network**: the network's name. **VLAN alias**, or "no alias". **Description**, cut to 80 characters. **Connected devices**, with their number: each device connected to this switch, with the address of the interface it connects on. |

A device or a switch with notes has a last row, **Notes**, with their number:
each note on one line, cut to 80 characters. A list shows its first 8
entries, then "+N more". An empty row says "None", and a device with no OS
type says "Not set".

The tooltip stays while the pointer is on the node or on the tooltip, and
while the node has keyboard focus. <kbd>Esc</kbd> closes it and keeps the
selection. A second <kbd>Esc</kbd> clears the selection. Notes, groups,
drawings and connections have no tooltip, and PNG and SVG downloads do not
show it. Screen readers read the same facts, the notes included, as the
node's description.

## Side columns

The left column, **Nodes & Templates**, holds **Nodes & Objects** and its
templates. The right column holds the **Inspector**. Beside each column are
two toggles:

- **Widen Nodes & Templates** and **Widen Inspector** make the column as
  wide as possible. Select the toggle again to go back.
- **Hide Nodes & Templates** and **Hide Inspector** fold the column into a
  narrow strip, which gives the canvas more room. The strip holds the
  toggle that shows the column again.

To set a width, drag the splitter between the column and the canvas
(**Resize Nodes & Templates**, **Resize Inspector**). Double-click it to go
back to the default width.

When you select a node or connection on the canvas, a hidden Inspector
shows. The browser keeps the widths and the hidden columns.

In a window narrower than about 900 pixels, the columns stack. The toolbar
and **Nodes & Templates** come first, then the canvas and the
**Inspector**. Every column shows, and the page scrolls.

![The editor in an 820-pixel-wide window: the header wraps, and the toolbar and Nodes & Templates stack in one column above the canvas and the Inspector.](../images/builder/narrow-layout.png)

## Nodes & Objects

**Nodes & Objects** lists what you can add: **Device**, **Switch**,
**Note**, **Group**, and the drawings **Rectangle**, **Circle**, **Icon**
and **Line** (see [Shapes, icons and lines](diagrams.md#shapes-icons-and-lines)).
Under **Templates**, it lists the device templates, such as **Server**,
**Workstation**, **Router**, **Firewall** and **External device**. Select
an item to add it to the diagram, or drag it onto the canvas. Builder puts
the new node in free space and selects it. Point at an item to see its
description. See [Adding devices](diagrams.md#adding-devices).

The templates are in sections, in this order:

1. **This diagram**: the templates that the diagram keeps.
2. **Library**: the templates of the server-wide library that are in no
   collection. When the library has collections, this section is named
   **Other library templates**.
3. One section for each collection: the collections of the library, then
   the server collections. A server collection starts with **Server:**,
   for example **Server: Example Plant Templates**.
4. **Built-in**: the built-in templates. This section always shows.

A section with no templates does not show. With two or more sections, each
section has a header with its name and its number of templates. Select a
header to collapse or expand its section. The browser keeps the collapsed
sections.

A template of **This diagram** can be a copy of a template of the library
or of a server collection. When that template has a newer version, a line
under the copy says **Update available**, with an **Update** button. When
the template was deleted from the library, the line says **Deleted from
library**, with an **Unlink** button. A server template that is gone says
**Removed from server**. The menu of the copy also has **Update** and
**Unlink**. **Update** asks first, and devices made from the template do
not change. See
[Templates in the editor](templates.md#templates-in-the-editor).

The **Templates** heading has the **New device template** (**+**) and
**Node Templates library** buttons (see
[Templates in the editor](templates.md#templates-in-the-editor)).

To find a node, use **Go to node** in the command palette (type **@** and
its name). To find a network, use **Go to network** (type **#**). To
rename a node, select it and press <kbd>F2</kbd>: the first field of the
Inspector, its name, takes focus. To remove a network with its switches,
use **Remove network** in the command palette. It also removes a network
that no switch shows (see
[Adding switches and networks](diagrams.md#adding-switches-and-networks)).

To connect a device to a switch, or to move a node into or out of a group,
without dragging, use **Add connection** and **Move to group** in the
header of the Inspector (see [The Inspector header](#the-inspector-header)).

## Inspector

The **Inspector** shows the fields of what is selected, and changes them.
It works on one node or one connection at a time. With nothing selected, or
with several items selected, it shows the diagram.

A text box for more than one line, such as **Description** or a note, is
as wide as the Inspector and two lines tall. It grows to fit the text you
type, up to eight lines, and then scrolls. To make it taller, drag its
bottom right corner down.

### The Inspector header

When one node is selected, the top of the Inspector names it, for example
"Device edge-rtr". At the right are these buttons:

- **Add connection**: opens the **Add a connection** dialog, which connects
  a device to a switch without dragging (see
  [Add a connection without dragging](diagrams.md#add-a-connection-without-dragging)).
  It shows for a device or a switch. It does not show for a device from an
  included topology.
- **Move to group**: opens the **Move to a group** dialog, which puts the
  node in a group, or removes it from its group, without dragging (see
  [Groups](diagrams.md#groups)).
- **Open Node Editor**: the pencil button, in the accent color. It opens
  the [Node Editor](#node-editor).

The three buttons have the same size. Each dialog starts from the
selection. Point at a button, or move the focus to it, to see its name and
its keys in a tooltip. **Add connection** and **Move to group** have no
keys until you give them some (see
[Customizing shortcuts](#customizing-shortcuts)). In a published diagram,
only **Open Node Editor** shows.

### Collapsing sections

A group of fields, such as **General**, **Hardware** or **Network**, can
collapse. So can a list, such as **Interfaces** or **Drives**, and each
entry of a list, such as "Interface 1: eth0". Select the name of the
section or entry to collapse it, and select it again to expand it. A
collapsed entry keeps its buttons to move, insert and remove it.

- A collapsed section or entry that holds a field with an error shows a
  cross in the error color after its name. Screen readers read "has
  errors".
- When you go to a field in a collapsed section, the section expands. You
  go to a field from the list of fields to fix, from **Go to** in the
  checks, or from the table of contents of the [Node Editor](#node-editor).
- When **Apply** or **Save now** stops because fields have errors, each
  collapsed section and entry with an error expands.
- The sections stay collapsed for the next node you select, until you
  leave the page. The Inspector, the Node Editor and the template editor
  each keep their own collapsed sections.
- **Reset view** expands every section and entry.
- A search in the Node Editor expands the sections that hold a match. When
  the search ends, the sections collapse again as they were (see
  [Searching the fields](#searching-the-fields)).

Point at the label of a field to see its help in a tooltip. For a field of
the node's phenix spec, the tooltip starts with the path of the field in
the topology YAML, in bold, for example `hardware.vcpus` or
`network.interfaces.0.address`.

### With nothing selected

![The Inspector with nothing selected: the diagram's Name and Description; Icon size Small (16 pixels) with its hint; Grid size 16 with its hint; Layers, with This diagram has 7 placeholder layers, Layer 0 to Layer 6, the list of the layers, and the Edit layers… button; Details, with Created Sep 29, 2026, 12:17 PM by e2e-admin, Last edited Oct 10, 2026, 5:47 AM by global-admin and Source file riverside-water.builder.json; the Annotations imported from Topology riverside-water (maintainer and purpose); Notes, with No notes. and an Add note button; and Scenarios, with the sub-heading Scenario riverside-water (2 apps, 6 hosts), its vrouter and ntp apps with their hosts, and an Edit scenarios button.](../images/builder/inspector-diagram.png){ width="352" }

The **Diagram** section has:

- **Name** and **Description** of the diagram. Select **Apply** to keep a
  change.
- **Template updates**: shows only when a template of the diagram is a
  copy of a library template, or of a server template, that needs
  attention. The notice lists each such template:
    - A template with a newer version in the library has **Update**. With
      two or more of them, **Update all** updates them all. **Update** asks
      first, then copies the newer version into the diagram, as one step of
      **Undo**. Devices made from the template do not change.
    - A template whose library template was deleted has **Unlink**, which
      keeps the copy and removes its link to the library.

    In a published diagram, the notice has no buttons. See
    [Templates in the editor](templates.md#templates-in-the-editor).
- **Icon size**: **Small**, **Medium** or **Large** (see
  [Icon size](diagrams.md#icon-size)). In a published diagram, the
  size shows as text. When your **Node icon size** setting is not **Off**,
  a note says "Your Node icon size setting (Large) overrides this on your
  canvas." The diagram keeps its size (see [Settings](#settings)).
- **Grid size**: the distance between the lines of the canvas grid, in
  pixels, 16 by default. Type a whole number from 4 to 200, then press
  <kbd>Enter</kbd> or leave the field. The change is one step of **Undo**.
  Other text shows "Grid size must be a whole number from 4 to 200." and
  changes nothing. Nodes go to this grid only while **Snap to grid** is on
  in the [Settings](#settings). In a published diagram, the size
  shows as text.
- **Layers**: how many layers the diagram has, or that it has the 7
  placeholder layers, and the list of the layers, each with its icon and
  color (see [Layers](diagrams.md#layers)). When the layers come from a
  layer set, a line says which set and which version, for example "From
  layer set Plant layers, version 3." When that set has a newer version,
  **Update from layer set** copies it into the diagram. **Edit layers…**
  opens the [layers editor](#layers-editor). Neither button shows in a
  published diagram.
- **Details**, which you cannot change here:
    - **Created**: when the diagram was made, and by whom.
    - **Last edited**: when the content you see was saved, and by whom. It
      changes with each change you make, after the server saves it. After
      **Undo** or **Restore**, it shows the save of the earlier version you
      went back to.
    - **Source file**: the name of the file the draft was made from, for a
      draft made by **Upload** of a file or by **Import** of a config
      file.

    A row does not show when the diagram does not have its value. In the
    picture, the uploaded file `riverside-water.builder.json` names
    `e2e-admin` as the maker of the diagram, and the upload is the last edit
    (see
    [Who made and last saved a diagram](import-upload-download.md#who-made-and-last-saved-a-diagram)).
- **Annotations**: for a draft imported from a config, the annotations of
  that config, under "From Topology riverside-water, imported" and the
  date. You cannot change them, and publishing does not write them. A
  diagram drawn in the editor has no such list. See
  [What an import keeps](import-upload-download.md#what-an-import-keeps).
- **Notes**: text about the diagram as a whole, one box for each note,
  or "No notes.". **Add note** adds an empty box and moves the focus to it.
  Builder saves a note when you leave its box, as one step of **Undo**. To
  remove a note, leave its box empty, or select its **Delete** button. A
  diagram holds at most 100 notes of at most 4096 bytes each (in UTF-8, so
  fewer characters outside ASCII). At 100, **Add note** is unavailable and
  says so. A note that is too long, or that holds a control character other
  than a line break or a tab, shows an error under its box. Builder does not
  save it, and the diagram keeps the note as it was until you fix the text.
  In a published diagram, the notes show as text. The notes are part
  of the diagram and go with it in Builder JSON and YAML, but publishing
  writes them to no config.
- **Scenarios**: "No scenarios." and **Add scenario**, or a sub-heading
  for each scenario the diagram lists. The sub-heading gives the number of
  apps of the scenario and the number of hosts they run on, for example
  "Scenario riverside-water (2 apps, 6 hosts)". The apps include the
  disabled apps. A host counts one time, also when several apps run on it.
  When the scenario has disabled apps, the sub-heading also gives their
  number, for example "Scenario nera (15 apps, 2 hosts, 1 disabled app)".
  Under the sub-heading, each app shows with the hosts it runs on. An app
  with no hosts shows "(no hosts)" after its name, and a disabled app
  "(disabled)". Builder reads the apps from the server, which needs
  `configs` `get` on the scenario. Without it, the Inspector says it cannot
  read them. **Edit scenarios** opens the **Scenarios** dialog (see
  [Scenarios](diagrams.md#scenarios)).

### Editing a node

![The Inspector for the device edge-rtr. Its header names Device edge-rtr and has the Add connection, Move to group and Open Node Editor (a pencil) buttons, all of one size. Below: Hostname, Icon Router, Icon size Diagram default (Small), Outline Color and Fill Color, then the Node heading with Type Router, and the General and Hardware sections, each with a disclosure arrow before its name, with the description VyOS edge router, Notes with No notes. and Add note, the drive vyos.qc2, 2048 MB of memory, OS type vyos and 2 VCPUs. Type, Memory, OS type and VCPUs each have a Default button, because they hold a value other than their default. The picture stops at the end of the Hardware section. Network, More settings, Layer with Edit layers…, Connection points and Position follow further down.](../images/builder/inspector-device.png){ width="352" }

Select a node to see its fields. To edit them in a large dialog, select
**Open Node Editor** at the top right of the Inspector (see
[Node Editor](#node-editor)). A device has these sections:

- **Hostname**, **Icon**, **Icon size**, **Outline Color** and **Fill
  Color** (see [The Icon field](#the-icon-field),
  [Colors](diagrams.md#colors) and [Icon size](diagrams.md#icon-size)).
  While your **Node icon size** setting is not **Off**, **Icon size** has a
  note that the setting overrides it on your canvas.
- **Node**, a heading over the fields of the node's phenix spec: **Type**,
  and **General** (**Description**, **Do not boot**, **Node hostname**,
  **Notes**, **Snapshot**, **VM type**). Each note has a text area of its
  own, and **Add note** adds one (see
  [Notes on devices and switches](diagrams.md#notes-on-devices-and-switches)).
- **Hardware**: **CPU**, **Drives** (**Image** and more for each drive),
  **Memory**, **OS type** and **VCPUs**.
- **Network**: **Interfaces**, **OSPF**, **Routes** and **Firewall
  rulesets**.
- **More settings**, a bar at the bottom of the node fields that you
  select to open: **miniccc commands**, **Delay**, **Injections**,
  **Advanced settings**, **Labels** and **Annotations**.
- **Layer**: the layer the device is at (see
  [Layers](diagrams.md#layers)). A device that has no layer chosen for it
  can be at the layer that its fields match. **Use matched layer** removes
  the chosen layer, so that the device returns to the matched layer.
  **Edit layers…** opens the [layers editor](#layers-editor).
- **Connection points**: the interfaces of the device, each with a connection
  point on the canvas.
- **Position**: **X** and **Y**, and **Move**.

Fields marked \* are required. An empty optional field shows the value phenix
uses in its place, marked as the default, for example "Default (kvm)" for
**VM type**. While a field that has a default holds another value, a
**Default** button shows beside it. Select **Default** to reset the field
to its default: Builder empties an optional field, and gives a required
field its default value. The focus returns to the field. As
with other changes, select **Apply** to keep it. Screen readers read the
button as, for example, "Reset Memory to the default, 512". The Node Editor
has the same button.

Changes in the Inspector take effect when you apply them:

1. Change one or more fields. A bar at the bottom of the Inspector says
   "Unapplied changes", with **Apply** and **Cancel**.
2. Select **Apply**, or press <kbd>Enter</kbd> in a field. The diagram
   changes, and the change is saved.

**Cancel** discards the changes. **Apply** is unavailable while a field you
changed has an error. The bar then says "Fix the fields marked with errors".
When the diagram changes while you edit (for example, a layout finishes),
the Inspector keeps the text you type and your unapplied changes.

For example, to change the description of ws-01:

1. On the canvas, select ws-01.
2. In **Description**, replace "Engineering workstation" with
   `Engineering workstation, CAD`.
3. Press <kbd>Enter</kbd>. The change is applied.

If you select another node before you apply, the Inspector applies your
changes when they are valid, and discards them when they are not. Applied
changes appear in Draft History, for example as "Applied changes to Device
ws-01". Builder also saves changes that you did not apply when you leave,
publish or download the draft (see
[Automatic snapshots](drafts.md#automatic-snapshots)).

Some changes take effect at once, without **Apply**:

- The fields in
  [Fields that take effect at once](#fields-that-take-effect-at-once).
- **Layer**. Each change is one step of **Undo**, for example
  "Changed the layer of Device plc-01 to Basic control".
- **Connection points**: adding, disconnecting or removing one.

The fields come from the phenix server, so they agree with what it accepts.
When the server cannot send them, the Inspector says so, for example "Could not
load this server's form fields. … The Inspector shows the fields built into
the Builder instead, which may differ from what this server accepts." See
[Permissions](administration.md#permissions).

### Other kinds of nodes

- A switch is one network. Its Inspector ("Network CORP") has **Name**,
  **VLAN alias**, **Subnet**, **Gateway**, **Description**, **Edge
  Color**, **Line style**, **Outline Color**, **Fill Color**, **Icon
  size**, **Notes**, **Layer**, **Addresses** and **Position**. See
  [Adding switches and networks](diagrams.md#adding-switches-and-networks)
  and [Notes on devices and switches](diagrams.md#notes-on-devices-and-switches).
  For **Subnet**, **Gateway** and **Addresses**, see
  [Addresses of a network](#addresses-of-a-network).
- A note has **Text**, **Color** and **Position** (see
  [Notes](diagrams.md#notes)).
- A group has **Title**, **Description**, **Color**, **Border pattern**,
  **Icon**, **Icon size** and **Position** (see
  [Groups](diagrams.md#groups)).
- A connection has **Label** (by default, the network's name), **Color** and
  **Line style**. Its Inspector names it, for example "Connection from
  ntp-01 (eth0) to CORP".
- A rectangle or a circle has **Shape**, **Label**, **Fill Color**,
  **Outline Color**, **Border pattern**, **Width** and **Height**.
- An icon has **Icon**, **Label**, **Width** and **Height**.
- A line has **Label**, **Color**, **Line style**, **Arrowhead at the start**,
  **Arrowhead at the end** and **Points**, each point's **X** and **Y** on
  the canvas (see
  [Shapes, icons and lines](diagrams.md#shapes-icons-and-lines)).

The **Layer** of a switch takes effect at once, as a device's does.

### Addresses of a network

The Inspector of a switch has two fields for the addresses of its
network. Publishing does not write them to the topology.

- **Subnet**: the IPv4 network, for example `10.10.20.0/24`. A static
  interface with no address that you connect to the network gets the next
  free address of the subnet.
- **Gateway**: the IPv4 address of the router on the network. It must be
  in the subnet. An interface that gets an address from the subnet also
  gets this gateway.

Under the fields, **Addresses** has the **Assign addresses** button. It
gives each static interface on the network that has no address the next
free address of the subnet, as one step of **Undo**. It works on the
subnet as the diagram has it, so apply a new **Subnet** first. When the
network has no subnet, the button is unavailable, and the hint says "This
network has no subnet. Apply a Subnet to assign addresses." **Assign
addresses** in the command palette does the same for a network that you
choose.

Builder announces what each connection does to the addresses:

- "Set the address of eth0 of ws-01 to 10.10.20.10/24."
- "No address set. Type an address for eth0 of ws-01 in the Inspector, or
  select Assign addresses." This occurs when **Assign addresses on
  connect** is off in the [Settings](#settings).
- "No address set: network CORP has no subnet. Type an address for eth0 of
  ws-01 in the Inspector."
- "No address set: subnet 10.10.20.0/24 of network CORP is full."

**Assign addresses** announces, for example, "Assigned 3 addresses on
network CORP". The same text names the step in Draft History.

### Fields that take effect at once

Some fields change how a node or a connection looks, not what it is. They
take effect at once, without **Apply**. The other fields wait for
**Apply**.

| Where | Fields that take effect at once |
|---|---|
| Device | **Icon**, **Icon size**, **Outline Color**, **Fill Color** |
| Switch | **Edge Color**, **Line style**, **Outline Color**, **Fill Color**, **Icon size** |
| Group | **Color**, **Border pattern**, **Icon**, **Icon size** |
| Note | **Color** |
| Rectangle or circle | **Fill Color**, **Outline Color**, **Border pattern** |
| Icon | **Icon** |
| Line | **Label**, **Color**, **Line style** |
| Connection | **Label**, **Color**, **Line style** |

Each change is one step of **Undo**, for example "Changed the fill color of
Device web-01 to #2f6fbf". A text field, such as **Label**, takes effect
when you press <kbd>Enter</kbd> or move the focus out of the field. To put
back the text that the field had before you typed, press <kbd>Esc</kbd>
before you press <kbd>Enter</kbd> or leave the field.

In the template editor, these fields change the template at once. **Save**
saves the template.

### The Icon field

**Icon** shows the icon of the node and its name. To change the icon:

1. Select **Icon**, or move the focus to it and press <kbd>↓</kbd>. A list
   of the built-in icons opens, with the focus on the icon in use.
2. Select an icon. To go to an icon, type its first letter.

![The Inspector of the device edge-rtr, with the Add connection, Move to group and Open Node Editor buttons in its header, and the Icon menu open below the Icon field, which shows Router. The menu shows Default (Server), Server, Desktop, Laptop, Smartphone, Printer, NTP Server, Router and Switch, each with its icon, and more icons below them. Router has a check mark.](../images/builder/icon-menu.png){ width="352" }

At the end of the list, **Custom icon…** opens the **Custom icons** dialog.
There you upload an image or choose one (see
[Custom icons](diagrams.md#custom-icons)). A custom icon in use shows its
name, followed by "(custom icon)". A built-in icon that you select replaces
the custom icon. <kbd>Esc</kbd> closes the list and changes nothing.

**Default**, at the start of the list, is the icon that the node shows when
it has no icon of its own, for example "Default (Server)". An icon drawing
has no **Default**. In a published diagram, **Icon** shows the icon,
and the list does not open.

### Fields you cannot change

Some fields are read only:

- A device from an included topology says "Included topology corp-services
  defines this device, so it is read only here. Change it in corp-services
  and import again, or combine the included nodes into a new draft to edit
  them here. You can still move it." Under the note, **Combine into a new draft**
  makes a copy of the diagram in which the included devices are its own (see
  [Making included devices editable](diagrams.md#making-included-devices-editable)).
- A network that an included device is on cannot be renamed. For CORP the
  Inspector says "Network CORP cannot be renamed: ntp-01 from included
  topology corp-services is on it. Its other fields can still change."
- Every field is read only in a published diagram (see
  [Published diagrams](drafts.md#published-diagrams)).

### Node Editor

The **Node Editor** shows the fields of one node in a large dialog. The
fields are in columns, so you do not have to widen or hide a side column to
edit a long form.

To open the Node Editor, do one of these steps:

- Select the node. At the top right of the Inspector, select **Open Node
  Editor** (the pencil). Its tooltip shows the key, for example "Open Node
  Editor (⌥⇧E)".
- Double-click the node on the canvas. A double-click on a line adds a bend
  and does not open the Node Editor.
- Select the node, or move the focus to it on the canvas. Then press
  <kbd>⌥</kbd>+<kbd>⇧</kbd>+<kbd>E</kbd> on macOS, or
  <kbd>Alt</kbd>+<kbd>Shift</kbd>+<kbd>E</kbd> on Windows and Linux.
- In the command palette, select **Open Node Editor**. With one node
  selected, it opens on that node. Otherwise, the palette asks for a
  device or a switch, found as **Go to node** finds it, and selects it.
  When the keys run the command with no single node selected, the palette
  opens at the same step.

The title names the node, for example "Edit device plc-01". In a published
diagram, the title is "Device plc-01". The Node Editor has the fields
that the Inspector shows for a device, a switch, a note, a group, a shape,
an icon or a line. It also shows the checks of the node, **Connection
points** and **Position**. **Layer** is a field after the colors, with
**Edit layers…**, which opens the [layers editor](#layers-editor) over the
Node Editor. The Node Editor stays open with your changes. The first field
has the focus.

![The Node Editor with the title Edit device plc-01. At the top right, the Search fields box with the placeholder Field name or path, then Expand all and Collapse all. At the left, the table of contents lists General, Notes (0), Hardware, Network, Interfaces (1), Interface 1: eth0, More settings, Labels (2), Annotations (0), Connection points and Position. At the right, in columns: Hostname plc-01, Icon External device, Icon size Diagram default (Small), Outline Color, Fill Color, and Layer None with its hint and Edit layers…. Below them: the Node heading with Type HIL, External ticked, and the General section with Description, Node hostname, Notes and VM type, where the scroll area ends. At the bottom are Cancel and Apply.](../images/builder/node-editor.png)

#### Sections and the table of contents

The Node Editor has these tools. **Expand all**, **Collapse all** and the
table of contents show for a device only:

- **Search fields**, at the top right, shows only the fields that match
  what you type (see [Searching the fields](#searching-the-fields)).
- **Expand all** and **Collapse all**, after the search, expand or collapse
  every section and entry of the form (see
  [Collapsing sections](#collapsing-sections)).
- The table of contents lists the sections, the lists in them and their
  entries, for example **Network**, **Interfaces (2)** and "Interface 1:
  eth0". It also lists **Connection points** and **Position**, and
  **Checks** when the node has errors or warnings.
  A list shows its number of entries. An entry with a field with an error
  shows a cross. Select an entry to expand it, scroll to it and move the
  focus to its name. The table of contents marks the entry in view. When
  all the fields fit without scrolling, it marks the entry you went to.
- In a window 900 pixels wide or more, the table of contents is a column
  at the left of the fields. In a narrower window, select **Table of
  contents** to show it above the fields. A choice in it closes it again.

The headers of the **General**, **Hardware** and **Network** sections, and
of **More settings** while it is open, stay at the top of the fields as you
scroll. A field that gets the focus scrolls clear of them.

![The Node Editor of edge-rtr after Collapse all. At the top right are the empty Search fields box, Expand all and Collapse all. The table of contents at the left lists General, Notes (0), Hardware, Drives (1), Drive 1: vyos.qc2, Network, Interfaces (3), Interface 1: eth0 to Interface 3: eth2, OSPF, Areas (0), Routes (1), Route 1: 10.10.30.0/24, Firewall rulesets (0), More settings, miniccc commands (0), Delay, C2 dependencies (0), Injections (0), Advanced settings (0), Labels (1), Annotations (1), Connection points and Position. At the right, below the Node heading and Type Router, the General, Hardware and Network sections are collapsed, and More settings is closed and lists miniccc commands, Delay, Injections, Advanced settings, Labels (1) and Annotations (1).](../images/builder/node-editor-collapsed.png)

#### Searching the fields

**Search fields** finds a field by its label or by its path in the topology
YAML. Type a label, such as `memory`, or a path, such as `hardware.os_type`
or `network.interfaces[0].vlan`. A part of a path matches too, for example
`interfaces.vlan`. Letter case does not count. When you type two or more
words, each word must match. The search does not look at the values of the
fields.

- The sections, lists and entries that hold a match expand, and the other
  fields do not show. A section, list or entry whose name matches, for
  example **Hardware**, **Drives** or "Interface 1: eth0", shows all of its
  fields.
- A line under the search box counts the fields that match, for example
  "3 fields match.", or says "No fields match."
- The table of contents lists only the parts that the search shows.
- To show all the fields again, empty the box, or press <kbd>Esc</kbd> in
  it. The sections that were collapsed before the search collapse again.
  When the box is empty, <kbd>Esc</kbd> closes the Node Editor.

For example, in the Node Editor of edge-rtr, type `vlan`. The **Network**
section shows the **VLAN** field of each interface, and the line under the
box gives their number.

![The Node Editor of edge-rtr with vlan in the Search fields box, and the line 3 fields match at the left, below the title. The table of contents lists Network, Interfaces (3) and Interface 1: eth0 to Interface 3: eth2. At the right, the Network section shows only Interface 1: eth0, Interface 2: eth1 and Interface 3: eth2, each with its VLAN field: INTERNET, DMZ and CORP.](../images/builder/node-editor-search.png)

When **Apply** stops because fields have errors, the search empties, and
the sections stay expanded so that you can see the fields with errors.

To edit a node in the Node Editor:

1. Change one or more fields.
2. Select **Apply**. Builder applies all the changes as one step of
   **Undo**, and closes the Node Editor. The focus goes back to the node on
   the canvas.

**Cancel**, <kbd>Esc</kbd> and **Close dialog** close the Node Editor. If
you changed fields and did not apply them, Builder asks "Discard changes to
this node?". Select **Keep editing** to go back to the fields, or
**Discard** to close the Node Editor without the changes.

Some changes take effect at once, as in the Inspector: the fields in
[Fields that take effect at once](#fields-that-take-effect-at-once),
**Layer**, **Connection points** and **Move**. **Cancel** does not
discard them. To remove them, use **Undo** after you close the Node Editor.

Before the Node Editor opens, the Inspector applies its unapplied changes,
as it does when you select another node. If a changed field of the
Inspector has an error, the Node Editor does not open, and the focus goes to
the list of fields to fix.

If you leave or reload the draft while the Node Editor has unapplied
changes, Builder applies and saves them, as it does for the Inspector (see
[Automatic snapshots](drafts.md#automatic-snapshots)).

In a published diagram, the Node Editor opens read only, with
**Close** only. The fields of a device from an included topology are read
only too.

## Checks and warnings

Builder checks the diagram as you edit it. The checks button in the
header shows the result: **No issues**, or a count such as **3 warnings**.

![The Diagram checks dialog of Riverside Water expansion: it says the diagram has 1 warning, which Publish lists as an error, and that drive images are not checked; under the heading 1 warning, the warning that interface eth0 of historian-01-2 is not connected to a network and has no VLAN, with Device historian-01-2, the code interface.vlan.missing and a Go to button; below it, the Preflight section with its four checks not ticked, the Experiment (optional) field and the Run button.](../images/builder/checks-dialog.png)

To see and fix the warnings of the Riverside Water expansion draft:

1. Open the Riverside Water expansion draft. The checks button says
   **1 warning**.
2. Select **1 warning**. The **Diagram checks** dialog lists the errors
   first, then the warnings. Each group has a heading that counts it, such
   as "1 warning". In each group, the issues follow the order of the nodes
   and connections they are about. Each issue names its device, switch or
   connection, and has a **Go to** button.
3. Select **Go to** beside the warning "interface "eth0" of
   "historian-01-2" is not connected to a network and has no VLAN, so it
   cannot be published: connect it, or type a VLAN for it". The dialog
   closes, the canvas selects historian-01-2, and the Inspector opens on it.
4. The Inspector lists the device's own warnings under "Checks: 1 warning".
   **Go to** beside a warning there moves focus to its field. Fix them there
   (see [What blocks publishing](publishing.md#what-blocks-publishing) for
   this example).

**Go to** shows the Inspector if it is hidden, and moves the view to the
node. An issue about a network goes to the network's switch. An issue that
names no field moves focus to its node or connection on the canvas. An issue
about the diagram as a whole, or about a network with no switch, has no
**Go to**.

There are two levels:

- **Errors**, such as a required field left empty. You cannot publish the
  diagram until you fix them.
- **Warnings**. Three kinds of warnings also block **Publish**, which lists
  them as errors (see
  [What blocks publishing](publishing.md#what-blocks-publishing)). The
  dialog says so, for example "The warnings must be fixed before the
  diagram can be published, and Publish lists those warnings as errors."
  Other warnings, such as a device with no interfaces, do not block
  publishing.

Each node with a problem has a warning mark on the canvas. Devices from
included topologies get no warnings: they belong to their own topology. This
diagram's devices are still checked against them: a hostname an included
device also uses is an error, and an address it also uses is a warning.

When the server lists no disk images, the dialog says "Drive images are not
checked: the server did not list its disk images." Otherwise, a drive image
that the server does not have is a warning.

### Preflight

The **Preflight** section of the **Diagram checks** dialog asks the phenix
server to check the saved draft against the server and its cluster, before
you start an experiment from it. The checks only read. They write nothing
and start no VM or experiment. At first, no check is ticked. This browser
keeps the checks you last ticked until you log out.

| Check | What it looks at |
|---|---|
| **Host capacity** | The CPUs and memory of each device that is not external (`hardware.vcpus` and `hardware.memory`, or 1 vCPU and 512 MB when unset), against what the schedulable cluster hosts have free. Each device must fit on one host, with its vCPUs and its memory in that host's CPUs and memory. A device that fits on no single host fails the check. Too little free memory also fails it. Too few free CPUs is a warning, because VMs can share CPUs. |
| **Networks** | Three comparisons. The VLANs of the devices and the VLAN aliases of the networks, against the VLAN range of the experiment you name. No range applies without an experiment, or when the experiment sets none. The aliases, against the VLANs of the running experiments that your role may list. When the check leaves out a running experiment that your role may not list, the summary says "running experiments your role may not list were not compared", and names none of them. The bridges the interfaces name, and the experiment's default bridge (`phenix` without an experiment), against the bridges of the schedulable hosts. A bridge that a host does not have yet is a warning, because minimega creates it when the experiment starts. |
| **Disk images** | Each drive image, by file name, against the server's disk images, and its kind: a VM or ISO image for a kvm device, a container image for a container. The server reuses its list of disk images for 10 seconds, so an image added in that time may not be found yet. |
| **Scenario apps** | Each app that the diagram's scenarios run (not the apps a scenario disables), against the default apps, the built-in apps and the user apps on the server's `PATH`. |

To check the Riverside Water expansion draft before starting it as the
`riverside` experiment:

1. Open the draft and select the checks button in the header.
2. Under **Preflight**, tick **Host capacity**, **Networks**, **Disk
   images** and **Scenario apps**.
3. Type `riverside` in **Experiment (optional)**. The field suggests the
   experiments the server lists.
4. Select **Run**. Builder saves your latest changes first, as **Publish**
   does. When the checks are done, the dialog says, for example,
   "Preflight: 3 passed, 1 unavailable", and lists each check with
   **Passed**, **Failed** or **Unavailable** and a summary, such as
   "Needed: 6 vCPUs and 6144 MB of memory for 4 devices. Free: 30 vCPUs and
   57344 MB on 2 schedulable hosts."
5. Each issue of a check has its code and, when it is about a device or a
   network, **Go to**, as the other issues do.

On a phenix server where minimega is not running, the report of step 4
says "Preflight: 1 passed, 3 unavailable":

![The Preflight section after Run, with the four checks ticked and riverside in Experiment (optional): it says Preflight: 1 passed, 3 unavailable. Host capacity: Unavailable, because the cluster hosts cannot be read; Networks: Unavailable, with 4 VLANs, no VLAN range in experiment riverside, and the bridges not checked; Disk images: Unavailable, because the server listed no disk images; each of these with its warning and the code preflight.unavailable; and Scenario apps: Passed, Found: 2 of 2 apps, named by 1 scenario.](../images/builder/preflight-report.png)

A check is **Unavailable** when Builder could not make it, or a part of it.
An issue gives one of these reasons:

- Your role does not have the permission that the check needs (see
  [Administration](administration.md#what-each-task-needs)).
- minimega cannot be reached.
- The server lists no disk images.
- The check took longer than 20 seconds.

Builder still makes the other checks. The report stays when the dialog
closes and opens again. Builder clears it when the diagram changes.

## Command palette

The command palette runs any command by name. It also finds nodes,
networks, node templates, template collections and custom icons.

![The command palette with the filter layout: and its 8 results, Layout: ELK layered, By layer, Network cards, Dagre, Standard, Yifan Hu, Force and Radial, with ELK layered marked as the current layout.](../images/builder/command-palette.png)

To open it, select **Commands** in the header, or press
<kbd>⌘</kbd>+<kbd>K</kbd> on macOS or <kbd>Ctrl</kbd>+<kbd>K</kbd> on Windows
and Linux. The keys also work in text fields. The palette also works on the
drafts page.

The field says "Type a command, @ for nodes, # for networks":

- Type words to find commands, for example `layout`. Commands are listed in
  groups such as **General**, **Structure**, **Add**, **Go to**, **View**
  and **Draft**. A command shows its keys, if it has any.
- When items are selected, the first group, for example "Selected:
  historian-01", holds commands for them, such as "Rename historian-01" and
  **Duplicate**.
- When you open the palette, a **Recent** group lists the commands you ran
  last.
- A command that does not apply now says why, for example "Unavailable:
  There is no earlier layout to restore."
- Some commands are also found by another word. Type `export` to list every
  download format.
- Each download format is a command of its own: **Download Builder JSON**,
  **Download Builder YAML**, **Download Topology YAML**, **Download PNG**,
  **Download SVG** and **Download Gephi (GEXF)**. Each opens the **Download
  diagram** dialog and starts that download at once (see
  [Downloading](import-upload-download.md#downloading)).
- A command marked **›** has a second step. For example, **Add device**
  then lists **Device** and every device template of
  [Nodes & Objects](#nodes-objects), each with its section and image, for
  example **Server** with "Built-in · ubuntu.qc2". Press
  <kbd>⌫</kbd> (<kbd>Backspace</kbd>) in the empty field to go back a step.
- Type `@` and text to find nodes. See [Finding nodes](#finding-nodes).
- Type `#` to list the networks, found by name or VLAN alias. Choose one to
  select its switch.
- Type `t:` to find [node templates](templates.md), `c:` to find template
  collections and `i:` to find [custom icons](diagrams.md#custom-icons).
  Type `?` to list all the prefixes.
- When you type words without a prefix, the palette also lists up to three
  templates, three collections and three icons that the words find. Each
  shows its prefix at the right.

Some commands to know:

| Command | Group | What it does |
|---|---|---|
| **Open Node Editor** | Edit | Opens the [Node Editor](#node-editor) on the selected node. With no single node selected, it asks for a device or a switch. |
| **Edit layers…** | General | Opens the [layers editor](#layers-editor). On the drafts page, it shows the **Layers** tab. |
| **Set layer of selection** | Structure | Asks for a layer, or **No layer**, and puts every selected device and switch at it, as one step of **Undo**. A device with **No layer** goes to the layer that its fields match, if any. Nodes from included topologies keep their layers. |
| **Select nodes on layer** | Selection | Asks for a layer, and selects every node at it: the layer chosen for the node, or the layer that its fields match. Each layer shows its number of nodes. |
| **Assign addresses** | Structure | Asks for a network with a subnet, and assigns addresses on it (see [Addresses of a network](#addresses-of-a-network)). |
| **Add a connection…** | Add | Opens the **Add a connection** dialog, as **Add connection** in the [Inspector header](#the-inspector-header) does. |
| **Move to a group…** | Structure | Opens the **Move to a group** dialog, as **Move to group** in the Inspector header does. |
| **Add switch** | Add | Adds a switch, as **Switch** in [Nodes & Objects](#nodes-objects) does. |
| **Show or hide node icons** | View | Turns the **Show node icons** setting on or off. |
| **Node icon size: Off**, **Small**, **Medium** and **Large** | View | Sets the **Node icon size** setting. |
| **Snap to grid** | View | Turns the **Snap to grid** setting on or off. |

The last three rows change the settings of the same names (see
[Settings](#settings)). Only **Open Node Editor** has keys by default. You
can give keys to the others (see
[Customizing shortcuts](#customizing-shortcuts)).

### Finding templates, collections and icons

The palette finds templates, collections and icons by their names and by
these fields. When a field matches and the name does not, the second line
shows the field, for example "Updated by: bob" or "In collection: Pumps".

| Prefix | Finds | Fields |
|---|---|---|
| `t:` | Node templates | Description, icon, custom icon, layer, type, hostname, image, collection ("In collection"), "Created by", "Updated by" and the source. The layer is "Layer 3" and its label, or the layer that the template's fields match in the open diagram. The source is "Library", "Server" or "Built-in". In the editor, the source is the section of the template in Nodes & Templates, for example "This diagram". |
| `c:` | Template collections | Description, the names of its templates ("Template"), "Created by", "Updated by" and the source: "Library collections" or "Server" |
| `i:` | Custom icons | Other names ("Also named"), size, "Created by", "Updated by" and the source: "Uploaded by" and the user, or "Server" |

"Created by" is the user who added the item. "Updated by" is the user who
changed the item last:

- For a template, the user who changed its name, description or device.
- For a collection, the user who changed its name, description or
  templates.
- For an icon, the user who renamed it.

A built-in template has neither. An item from a server collection has
neither. An icon that the server added has no "Created by". It has
"Updated by" only after a user renames it. An icon from before phenix kept
these names shows its uploader as "Created by" and has no "Updated by".

What a choice does:

- **Template, in the editor**: adds a device made from the template, as
  when you select the template under **Templates**. The editor offers the
  templates of the sections of [Nodes & Objects](#nodes-objects).
- **Template, on the drafts page**: opens the template in the template
  editor. A library template opens for changes. A built-in template and a
  template of a server collection always open read only.
- **Collection**: shows the collection on the **Node Templates** tab, and
  the **Show** field takes focus.
- **Icon**: opens the preview of the icon on the **Icons** tab.

From the editor, a collection or an icon first closes the editor, as the
**Drafts** button does.

Press <kbd>↑</kbd> and <kbd>↓</kbd> to move, <kbd>Enter</kbd> to run, and
<kbd>Esc</kbd> to close. On a node, <kbd>Shift</kbd>+<kbd>Enter</kbd> adds it
to the selection.

### Finding nodes

Type `@` and some text to find nodes. The palette finds a node by its
name, and by these fields, in this order:

1. Hostname
2. IP address
3. MAC address
4. VLAN or network, with its VLAN alias
5. Annotation
6. Label
7. Description
8. Note
9. Image

The nodes whose names match come first. The nodes that a field finds come
after them, in the order of the fields. When a field matches and the name
does not, the second line starts with the field. An address shows its
interface, for example "eth0 IP address 10.10.30.20". A label or a note
shows its kind, for example "label role=historian". Type `?` in the
palette to see the fields again.

For example, `@10.10.30.20` finds historian-01, with "eth0 IP address
10.10.30.20" on its second line.

`@10.10.20` finds each node with an address in 10.10.20:

![The command palette with @10.10.20 and, under NODES, edge-rtr, files-01, ot-fw, ws-01, ws-02, dns-01 and ntp-01. The second line of each starts with the field that matched, for example eth2 IP address 10.10.20.1 for edge-rtr, with the matched text underlined, then the kind, the image, the group and the networks. dns-01 and ntp-01 add included from corp-services, read only. The footer shows the keys to move, run, add to selection and close, and 7 nodes.](../images/builder/command-palette-search.png){ width="592" }

To find the historian by name:

1. Press <kbd>⌘</kbd>+<kbd>K</kbd> (<kbd>Ctrl</kbd>+<kbd>K</kbd>).
2. Type `@hist`. The palette lists historian-01 ("Device · ubuntu.qc2 · in
   OT · OT") and the note that names it.
3. Press <kbd>Enter</kbd>. The canvas selects historian-01, and the
   Inspector shows it.

<kbd>⇧</kbd>+<kbd>⌘</kbd>+<kbd>O</kbd> (<kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>O</kbd>)
opens the palette at **Go to node**, which lists every node.

## Keyboard shortcuts

To see every shortcut, select **Shortcuts** in the header, or press
<kbd>?</kbd> outside a text field. The **Shortcuts** button shows the key
that opens the sheet. The sheet shows the keys of your platform ("Keys for
macOS, and where each one works." or "Keys for Windows and Linux, and where
each one works."). Type in **Filter shortcuts** to find an action, or a key
such as `G`.

![The Keyboard shortcuts sheet with Single-key shortcuts on and its hint that ?, N, =, +, − and ⇧1 work alone, the Change shortcuts button, the Filter shortcuts field, and the groups in three columns: General and Edit, which ends with Open Node Editor (⌥⇧E); Selection; Structure, Add (Add device, N), Go to, View and Draft, with their macOS keys.](../images/builder/shortcuts-sheet.png)

The default shortcuts:

| Action | Where it works | macOS | Windows and Linux |
|---|---|---|---|
| Command palette | Editor and Drafts page, text fields too | <kbd>⌘</kbd>+<kbd>K</kbd> | <kbd>Ctrl</kbd>+<kbd>K</kbd> |
| Keyboard shortcuts | Editor and Drafts page, not in text fields | <kbd>?</kbd> | <kbd>?</kbd> |
| Settings… | Editor and Drafts page, not in text fields | <kbd>⌥</kbd>+<kbd>⇧</kbd>+<kbd>S</kbd> | <kbd>Alt</kbd>+<kbd>Shift</kbd>+<kbd>S</kbd> |
| Undo | Editor, not in text fields | <kbd>⌘</kbd>+<kbd>Z</kbd> | <kbd>Ctrl</kbd>+<kbd>Z</kbd> |
| Redo | Editor, not in text fields | <kbd>⇧</kbd>+<kbd>⌘</kbd>+<kbd>Z</kbd> | <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>Z</kbd> or <kbd>Ctrl</kbd>+<kbd>Y</kbd> |
| Copy | Editor, not in text fields | <kbd>⌘</kbd>+<kbd>C</kbd> | <kbd>Ctrl</kbd>+<kbd>C</kbd> |
| Paste | Editor, not in text fields | <kbd>⌘</kbd>+<kbd>V</kbd> | <kbd>Ctrl</kbd>+<kbd>V</kbd> |
| Duplicate | Editor, not in text fields | <kbd>⌘</kbd>+<kbd>D</kbd> | <kbd>Ctrl</kbd>+<kbd>D</kbd> |
| Delete selection | Canvas | <kbd>⌫</kbd> or <kbd>⌦</kbd> | <kbd>Delete</kbd> or <kbd>Backspace</kbd> |
| Rename | Canvas | <kbd>F2</kbd> | <kbd>F2</kbd> |
| Open Node Editor | Editor, not in text fields | <kbd>⌥</kbd>+<kbd>⇧</kbd>+<kbd>E</kbd> | <kbd>Alt</kbd>+<kbd>Shift</kbd>+<kbd>E</kbd> |
| Select all | Editor, not in text fields | <kbd>⌘</kbd>+<kbd>A</kbd> | <kbd>Ctrl</kbd>+<kbd>A</kbd> |
| Clear selection | Canvas | <kbd>esc</kbd> | <kbd>Esc</kbd> |
| Select or deselect the focused item | Canvas | <kbd>↩</kbd> or <kbd>Space</kbd> | <kbd>Enter</kbd> or <kbd>Space</kbd> |
| Add the focused item to the selection, or remove it from the selection | Canvas | <kbd>⇧</kbd>+<kbd>↩</kbd> | <kbd>Shift</kbd>+<kbd>Enter</kbd> |
| Move to the nearest node in the direction of the arrow key | Canvas | <kbd>←</kbd> <kbd>↑</kbd> <kbd>→</kbd> <kbd>↓</kbd> | <kbd>←</kbd> <kbd>↑</kbd> <kbd>→</kbd> <kbd>↓</kbd> |
| Move through the focused node’s connections | Canvas | <kbd>⇟</kbd> or <kbd>⇞</kbd> | <kbd>PgDn</kbd> or <kbd>PgUp</kbd> |
| Move the selected nodes 10 pixels | Canvas | <kbd>⇧</kbd> with an arrow key | <kbd>Shift</kbd> with an arrow key |
| Resize the selected group, note, shape or icon 10 pixels | Canvas | <kbd>⌥</kbd>+<kbd>⇧</kbd> with an arrow key | <kbd>Alt</kbd>+<kbd>Shift</kbd> with an arrow key |
| Group selection | Editor, not in text fields | <kbd>⌘</kbd>+<kbd>G</kbd> | <kbd>Ctrl</kbd>+<kbd>G</kbd> |
| Ungroup | Editor, not in text fields | <kbd>⇧</kbd>+<kbd>⌘</kbd>+<kbd>G</kbd> | <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>G</kbd> |
| Auto-group by network | Editor, not in text fields | <kbd>⌥</kbd>+<kbd>⇧</kbd>+<kbd>G</kbd> | <kbd>Alt</kbd>+<kbd>Shift</kbd>+<kbd>G</kbd> |
| Auto layout | Editor, not in text fields | <kbd>⌥</kbd>+<kbd>⇧</kbd>+<kbd>L</kbd> | <kbd>Alt</kbd>+<kbd>Shift</kbd>+<kbd>L</kbd> |
| Add device | Canvas | <kbd>N</kbd> | <kbd>N</kbd> |
| Go to node | Editor, not in text fields | <kbd>⇧</kbd>+<kbd>⌘</kbd>+<kbd>O</kbd> | <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>O</kbd> |
| Zoom in | Canvas | <kbd>=</kbd> or <kbd>+</kbd> | <kbd>=</kbd> or <kbd>+</kbd> |
| Zoom out | Canvas | <kbd>−</kbd> | <kbd>−</kbd> |
| Fit diagram to view | Canvas | <kbd>⇧</kbd>+<kbd>1</kbd> | <kbd>Shift</kbd>+<kbd>1</kbd> |
| Focus mode | Editor and Drafts page, text fields too | <kbd>⇧</kbd>+<kbd>⌘</kbd>+<kbd>F</kbd> | <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>F</kbd> |
| Save now | Editor, text fields too | <kbd>⌘</kbd>+<kbd>S</kbd> | <kbd>Ctrl</kbd>+<kbd>S</kbd> |

<kbd>N</kbd> on the canvas adds a plain Device, as **Device** under **Nodes
& Objects** does, at a free spot in the visible part of the canvas. Builder
selects the new device and moves focus to it, so <kbd>N</kbd> again adds
another. In the command palette, **Add device** asks for a template first.
In a published diagram, <kbd>N</kbd> adds nothing.

On a Mac keyboard without these keys, <kbd>⇟</kbd> and <kbd>⇞</kbd> are
<kbd>Fn</kbd> with <kbd>↓</kbd> and <kbd>↑</kbd>, and <kbd>↖</kbd> and
<kbd>↘</kbd> (Home and End) are <kbd>Fn</kbd> with <kbd>←</kbd> and
<kbd>→</kbd>. <kbd>⌫</kbd> is Delete and <kbd>⌦</kbd> is Forward Delete.

Other commands have no keys until you give them some (see
[Customizing shortcuts](#customizing-shortcuts)). Some examples:

- **Reset view** and **Download…**
- **Add a connection…** and **Move to a group…**, which the buttons in the
  [Inspector header](#the-inspector-header) also open
- **Add switch**
- **Show or hide node icons**, **Snap to grid**, and **Node icon size:
  Off**, **Small**, **Medium** and **Large**
- **Set layer of selection** and **Select nodes on layer**

"Not in text fields" means the keys do nothing while focus is in a field
you type in, or in any field of the Inspector. On the drafts page,
**Settings…** and **Keyboard shortcuts** also work while a card's checkbox
or **Select all** has focus.

### Keys on the canvas

The diagram is one <kbd>Tab</kbd> stop. The canvas keys of the table work on
the canvas itself, on its nodes and on its connections, not on the zoom
buttons. The table does not show these details:

- From the canvas itself, the arrow keys go to the node nearest the middle
  of the view.
- When the focused node shows its [info tooltip](#info-tooltips), the first
  <kbd>Esc</kbd> closes the tooltip, and the second clears the selection.
- <kbd>Delete</kbd> deletes the focused item, or the whole selection when
  the focused item is part of it.
- <kbd>F2</kbd> moves focus to the name of the focused item in the
  Inspector.
- With <kbd>Alt</kbd>+<kbd>Shift</kbd> (<kbd>⌥</kbd>+<kbd>⇧</kbd>),
  <kbd>→</kbd> and <kbd>↓</kbd> make the item larger, and <kbd>←</kbd> and
  <kbd>↑</kbd> make it smaller. With the mouse, drag the handles on its
  corners and sides.
- For an exact position, use **Position** in the Inspector.
- A second <kbd>Shift</kbd>+<kbd>1</kbd> (<kbd>⇧</kbd>+<kbd>1</kbd>)
  restores the view from before the fit.

In a published diagram, you can select items and copy them, but not
change them.

To add nodes or connect them without a pointer, use **Nodes & Objects** and
**Add connection** in the Inspector header. With a screen reader, use its focus mode (forms mode in
JAWS) for the canvas keys. You can also find a node with **Go to node** in
the command palette (see [Accessibility](#accessibility)).

A key you removed does nothing, and so does a single key while
[Single-key shortcuts](#single-key-shortcuts) are off. The tooltips, the
palette and the sheet show the keys you have.

### Single-key shortcuts

<kbd>?</kbd>, <kbd>N</kbd>, <kbd>=</kbd>, <kbd>+</kbd>, <kbd>−</kbd> and
<kbd>⇧</kbd>+<kbd>1</kbd> work alone, without <kbd>⌘</kbd> or
<kbd>Ctrl</kbd>. Disable **Single-key shortcuts** in the sheet or in
Settings if speech input or your screen reader can press them by mistake.
The palette and the buttons still run those commands.

### Customizing shortcuts

You can give any command other keys, or none. For example, to run
**Reset view** with <kbd>⌥</kbd>+<kbd>⇧</kbd>+<kbd>R</kbd>
(<kbd>Alt</kbd>+<kbd>Shift</kbd>+<kbd>R</kbd> on Windows and Linux):

1. Select **Shortcuts** in the header.
2. Select **Change shortcuts**. The sheet is now named **Change keyboard
   shortcuts**, and it lists every command, with or without keys.
3. In **Filter shortcuts**, type `reset view`.
4. On the **Reset view** row, select **Change**. The row says "Press the
   new shortcut".
5. Press <kbd>⌥</kbd>+<kbd>⇧</kbd>+<kbd>R</kbd>. The row shows the keys and
   says they are free: "Press Enter to keep it, or Escape to cancel."
6. Press <kbd>Enter</kbd>, or select **Keep**.
7. Select **Done**, then close the sheet.

The sheet after step 5:

![The Change keyboard shortcuts sheet filtered by reset view: the Reset view row shows Option Shift R in its field, with Keep and Cancel, and says the keys are free.](../images/builder/shortcuts-customize.png)

Now zoom the canvas, widen the Inspector, click an empty spot of the canvas,
and press <kbd>⌥</kbd>+<kbd>⇧</kbd>+<kbd>R</kbd>: the zoom and the columns
go back to how a diagram opens.

When you press keys that another command uses, the row says so, for
example that <kbd>⌘</kbd>+<kbd>G</kbd> "already runs Group selection". Select
**Use for** and the command's name, for example **Use for Reset view**, to
move the keys to this command, or press other keys. You cannot choose
keys the browser keeps, such as <kbd>⌘</kbd>+<kbd>T</kbd>.

Each changed row has **Remove** (no keys) and **Reset** (its default keys).
**Reset all shortcuts** puts every command back to its default keys. The
tooltips and the palette show your keys. The browser keeps them after you
log out.

## Settings

Select **Settings** in the header to open **Builder settings**, or press
<kbd>⌥</kbd>+<kbd>⇧</kbd>+<kbd>S</kbd> on macOS or
<kbd>Alt</kbd>+<kbd>Shift</kbd>+<kbd>S</kbd> on Windows and Linux, in the
editor or on the drafts page. Changes apply at once. The browser keeps the
settings after you log out. The settings change how Builder shows and edits
diagrams for you. They do not change the diagram.

![The Builder settings dialog. Appearance: Theme System, Light or Dark, and Reduce motion. Canvas: Default layout set to ELK layered, Show the minimap, Show node notes, Show node icons, Node icon size with Off, Small, Medium and Large and Off selected, Show coordinates, and Zoom when a diagram opens with 100%, Fit the whole diagram in view and Custom with a percentage field. Editing: Snap to grid, off, and Assign addresses on connect, on, each with its hint. Keyboard: Single-key shortcuts and Change keyboard shortcuts. Layers: Edit layers… with the hint The layers of a diagram are not a setting. The diagram keeps them, and the layers editor changes them. At the bottom, Reset to defaults and Done.](../images/builder/settings-dialog.png)

The settings are in these groups:

- Appearance:
    - **Theme**: **System**, **Light** or **Dark**. System follows your
      device's light or dark appearance.
    - **Reduce motion**: the canvas pans and zooms at once, and nothing
      animates. Motion is also reduced whenever your device asks for it.
- Canvas:
    - **Default layout**: one of the [layouts](diagrams.md#layouts), **ELK
      layered** by default. **Auto layout** and **Auto-group** use it on a
      draft whose layout menu says **Default**, and the draft then keeps
      it. A change of this setting moves no node.
    - **Show the minimap**: on by default. The toolbar's **Minimap** button
      shows or hides it until the next diagram opens.
    - **Show node notes**: on by default. Devices and switches show their
      notes below them on the canvas and in PNG and SVG downloads, and
      **Auto layout** leaves room for them (see
      [Notes on devices and switches](diagrams.md#notes-on-devices-and-switches)).
      **Show or hide node notes** in the command palette changes it too.
    - **Show node icons**: on by default. Devices, switches and groups show
      their icons on the canvas and in PNG and SVG downloads. The diagram
      keeps its icons when this is off. **Show or hide node icons** in the
      command palette changes it too.
    - **Node icon size**: **Off** (the default), **Small**, **Medium** or
      **Large**. **Off** uses the icon sizes of the diagram. Another size
      replaces the sizes of every device, switch and group on your canvas
      and in your PNG and SVG downloads. The diagram does not change, and
      other users see its sizes. While it is not **Off**, each **Icon
      size** field of the Inspector says that the setting overrides it. The
      **Node icon size** commands in the command palette change it too.
    - **Show coordinates**: off by default. Rulers along the top and left
      edges of the canvas show X and Y in the units of **Position** (see
      [Canvas](#canvas)).
    - **Zoom when a diagram opens**: **100%** (the default), **Fit the
      whole diagram in view**, or **Custom** with a percentage from 20 to
      200. Builder rounds a percentage to a multiple of 5, and typing one
      chooses **Custom**. A number out of range shows "Enter a number from
      20 to 200." A custom zoom first shows the same part of the diagram as
      100% does. **Reset view** also returns to it.
- Editing:
    - **Snap to grid**: off by default. When it is on, nodes that you drag
      or drop, new nodes and the points of lines go to the nearest point of
      the diagram's grid. When it is off, they go where you put them. Each
      diagram has its own **Grid size** (see
      [With nothing selected](#with-nothing-selected)). **Snap to grid** in
      the command palette changes it too.
    - **Assign addresses on connect**: on by default. When you connect,
      paste or duplicate a static interface that has no address, it gets
      the next free address of the network's subnet. When this is off, the
      interface stays without an address. **Assign addresses** works either
      way (see [Addresses of a network](#addresses-of-a-network)).
- Keyboard:
    - **Single-key shortcuts**: see
      [Single-key shortcuts](#single-key-shortcuts).
    - **Change keyboard shortcuts**: see
      [Customizing shortcuts](#customizing-shortcuts).
- Layers:
    - **Edit layers…**: the layers of a diagram are not a setting. In the
      editor, this button closes the settings and opens the
      [layers editor](#layers-editor) of the open diagram. On the drafts
      page, it shows the **Layers** tab, where you change the layer sets of
      the server and choose the default set of the server (see
      [The drafts page](drafts.md#the-drafts-page)).

**Reset to defaults** puts every setting back. It does not change the
layers of a diagram, the default layer set, or custom shortcut keys.
**Reset all shortcuts** in the shortcuts sheet resets the keys. Select
**Done** to close the dialog.

## Layers editor

The layers editor changes the layers of the open diagram. Its title is
**Layers of this diagram**. To open it, do one of these steps:

- In the Inspector, select **Edit layers…** under **Layers** in the
  Diagram section, or beside the **Layer** of a device or a switch.
- In the Node Editor, select **Edit layers…** beside **Layer**. The layers
  editor opens over the Node Editor, which keeps your unapplied changes.
- In **Builder settings**, select **Edit layers…**.
- In the command palette, select **Edit layers…**.

The layers editor has these parts:

- **Layer set**: says if the layers come from a layer set of the server,
  and which version. It has the buttons that apply now: **Update from layer
  set**, **Save to layer set**, **Save as new set…** and **Unlink**. Under
  them, **Use a layer set** and **Apply** replace the layers of the diagram
  with a copy of a set and link the diagram to it. Nodes keep the layers
  chosen for them.
- **Options**: **When a device matches more than one layer** (**Lowest
  layer ID wins** or **Highest layer ID wins**), and **Color node outlines
  with the layer color**.
- **Layers**: one row for each layer, with its **ID**, **Icon**,
  **Label**, color and **Node matchers**. **Add layer** adds a row.

Each change is one step of **Undo**. A diagram that has the placeholder
layers saves them with your first change. Select **Done** or press
<kbd>Esc</kbd> to close the layers editor. In a published diagram,
the layers editor shows the layers and you cannot change them. See
[Layers](diagrams.md#layers) for the layer fields, the node matchers and
the layer sets.

## Focus mode

Focus mode hides the phenix navigation bar, so Builder fills the
window. Select **Focus mode** in the header, or press
<kbd>⇧</kbd>+<kbd>⌘</kbd>+<kbd>F</kbd>
(<kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>F</kbd>). The button is then named
**Exit focus mode**. Select it, or press the same keys, to show the
navigation bar again.

Focus mode stays on when you go between the drafts page and the editor. It
ends when you leave Builder.

## Themes

Builder has a light and a dark theme. The theme button in the header
shows the theme in use: **System**, **Light** or **Dark**. Each press
switches to the next one, and its tooltip says which, for example "Theme:
System. Switch to Dark theme". On a device with a light appearance, the
order is System, Dark, Light. On a dark one, it is System, Light, Dark.
**Theme** in [Settings](#settings) sets the same choice.

![The Builder editor in the dark theme showing the Riverside Water diagram.](../images/builder/editor-dark.png)

The theme changes Builder only, not the rest of phenix. PNG and SVG
downloads use the background of the theme in use.

## Accessibility

You can use Builder with a keyboard only, and with a screen reader.

- Press <kbd>Tab</kbd> from the top of the page to reach **Skip to diagram
  canvas**, which moves focus to the canvas.
- The diagram is one <kbd>Tab</kbd> stop, with its own keys (see
  [Keys on the canvas](#keys-on-the-canvas)).
- A device or a switch with keyboard focus shows its
  [info tooltip](#info-tooltips), and screen readers read the same facts as
  its description. While the node has focus, the pointer resting on another
  node shows that node's tooltip, and the focused node's comes back when the
  pointer leaves. <kbd>Esc</kbd> closes the tooltip until focus moves.
- The toolbar is also one <kbd>Tab</kbd> stop. The arrow keys,
  <kbd>Home</kbd> and <kbd>End</kbd> move between its buttons. The counts in
  the header are also one stop (see [Selecting by kind](#selecting-by-kind)).
- A custom icon is decoration: a node's name, its type and what screen
  readers say of it do not change with its icon.
- To add nodes and connect them without a pointer, use **Nodes & Objects**
  and **Add connection** in the [Inspector header](#the-inspector-header).
  **Move to group** puts a node in a group. The
  [command palette](#command-palette) opens the same dialogs:
  type `connect` for **Add a connection…** or `group` for **Move to a
  group…**. Each dialog starts from the selection. <kbd>Esc</kbd> closes it
  and moves focus back to where you opened it.
- A screen reader announces what each change did, for example "Updated
  device ws-01.", or what a connection did to an address (see
  [Addresses of a network](#addresses-of-a-network)). A change of a
  setting from the command palette is announced too, for example "Snap to
  grid is on." or "Node icons are Large on your canvas." With a screen
  reader, use its focus mode (forms mode in
  JAWS) for the canvas keys. You can also find a node with **Go to node** in
  the command palette.
- **Reduce motion** in Settings stops the animations.
- In a narrow window, or at a high browser zoom, the columns stack (see
  [Side columns](#side-columns)).

Builder aims to meet WCAG 2.2 level AA.
