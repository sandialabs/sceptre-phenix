# Node Templates

A device template holds the fields of a new device. Builder fills in these
fields before you add the device: icon, custom icon, icon size, outline and
fill colors, layer, and the whole node spec (type, hardware,
interfaces and the rest).
**Nodes & Objects** lists the templates under **Templates**. To add a device
made from a template, select the template, or drag it onto the canvas (see
[Adding devices](diagrams.md#adding-devices)).

Templates are kept in four places:

- **In a diagram.** A diagram template is part of the Builder document, so
  it goes everywhere that the diagram goes: other tabs, the other users who
  open the draft, its downloads and its publications. A diagram template can
  be a copy of a library template or of a server template, linked to it. See
  [Diagram templates](#diagram-templates) and
  [Linked templates](#linked-templates).
- **In the template library.** The phenix server keeps one template library
  for all of its users. Its templates show in every diagram that each user
  opens. You manage it on the **Node Templates** tab of the drafts page. See
  [The template library](#the-template-library).
- **Built in.** phenix has five built-in templates. They are read only, and
  every diagram lists them. See [Built-in templates](#built-in-templates).
- **On the phenix server.** An administrator can put template files in the
  template directory of the server. phenix reads each file at start as a
  read-only collection that everyone sees. See
  [Server collections](#server-collections).

Template files move templates between these places. **Export** saves
templates as a YAML file, and **Import templates** reads a file into the
library. See [Exporting and importing templates](#exporting-and-importing-templates).

The description of a template is only its tooltip under **Templates**. A device
made from a template gets no description. A device keeps no link to its
template. If you change, update or delete a template later, no device changes.

## Templates in the editor

![Nodes & Objects with Device, Switch, Note, Group, Rectangle, Circle, Icon and Line, then the Templates heading with the New device template (+) and Node Templates library buttons. Each section name is a button with an arrow and the number of its templates. Under This diagram, the template Engineering workstation has its menu open, with Edit, Save to library and Delete. Under Riverside lab, a collection of the library, are Server, Workstation, Router, Firewall and External device, partly behind the menu. Under Server: Example Plant Templates are PLC, HMI and Plant router.](../images/builder/palette-templates.png){ width="240" }

Under **Templates**, **Nodes & Objects** lists the device templates in
sections, in this order:

| Section | Templates |
|---|---|
| **This diagram** | The templates saved in the diagram, linked templates included (see [Linked templates](#linked-templates)). |
| **Library** or **Other library templates** | The library templates that are in no collection. The section is **Other library templates** when the library has collections. |
| *Each collection* | One section for each collection: the collections of the library, then the server collections. A server collection starts with **Server:**, for example **Server: NLR Node Templates** (see [Server collections](#server-collections)). |
| **Built-in** | The five built-in templates, always (see [Built-in templates](#built-in-templates)). |

A template in two collections shows in both sections. A collection with no
template does not show. Section names show only when two or more sections
have templates.

Each section name is a button that collapses or expands the section. It
shows the number of templates in the section. The browser keeps the
collapsed sections until you log out.

Point at a template to see its description. The tooltip of a template of a
server collection adds "From the server collection", the name of the
collection, and "which is read only". The tooltip of a linked template that
has a mark adds the reason for the mark (see
[Linked templates](#linked-templates)).

A template is a saved device. Select a template to add a device, or drag
it onto the canvas. To make and edit library templates, use the **Node
Templates** tab of the drafts page.

Two buttons follow the **Templates** heading:

- **New device template** (the **+**) makes a template of this diagram (see
  [Making a diagram template](#making-a-diagram-template)).
- **Node Templates library** leaves the draft, as **Drafts** does,
  and shows the **Node Templates** tab of the drafts page. It also works in a
  draft that you can only view.

A device made from a template has these properties:

- Its name is the hostname of the template. When that name is taken, a
  number is added, for example plc and then plc-2.
- It has a connection point for each interface that the template names, and
  it is connected to nothing. A VLAN that names a network of the diagram is
  emptied. Other VLAN text is kept.
- It names the custom icon of the template, which the icon library of the
  server holds (see [Custom icons](diagrams.md#custom-icons)).
- It draws its icon at the icon size of the template. When the template
  names no size, it uses the size of the diagram (see
  [Icon size](diagrams.md#icon-size)).
- It has the layer ID of the template, if the template has one (see
  [Layers](diagrams.md#layers)). When the diagram does not have that layer,
  it shows as "Layer N".

When you add a device from a library template or a server template, Builder
also adds a copy of the template to **This diagram**, linked to it. Both are
one change, for example "Added device and template PLC". Builder adds the
copy only once: when the diagram already has a linked copy of that template,
it adds only the device. A built-in template gives no copy. When the diagram
has the most templates it can hold, Builder adds the device without a copy,
and says so: "Added device. This diagram has 50 templates, the most it can
hold. The device has no link to template PLC."

In the command palette, **Add device** lists **Device**, then each template
once, by section. The second line gives the section and the image, for
example "Library · ubuntu.qc2" or "This diagram · no image".

To find a template by more than its name, type `t:` in the command palette,
then part of one of its fields: description, icon, custom icon, layer, node
type, hostname, image or collection, or the user who created or last
changed it (see [Command palette](editor.md#command-palette)). In the editor,
a template with no layer of its own is found by the layer that its fields
match in the open diagram. Choose the template to add a device made from it.

### When the library cannot be loaded

While Builder reads the library for the first time, **Templates** says
"Loading the template library…". When Builder cannot read the library,
**Templates** says "The template library could not be loaded." and shows
**Retry**. The diagram templates and the built-in templates stay listed, so
you can still build a diagram. When a read succeeds, the templates of the
library show again. If a later read fails, the library from the earlier read
stays listed.

When the phenix server holds a library that it cannot read (for example,
because a newer version of phenix saved it), **Templates** says "The Builder
cannot read the template library. A newer version of phenix may have saved
it." with no **Retry**. The **Node Templates** tab says the same, and still
lists the built-in templates and the server collections. Open the library
with the phenix version that saved it.

Builder reads the library when it opens, and again:

- Each time it reads the drafts lists.
- When a draft opens in the editor.
- When the browser window returns to the front while the editor is open.
- After you sign in again.
- After each change that you make to the library.

## Diagram templates

### Making a diagram template

To save ws-01 of Riverside Water as a template of the diagram:

1. Select ws-01 on the canvas.
2. Select **New device template** (the **+** beside **Templates**),
   or run **New device template** from the command palette. The template
   editor opens, titled **New device template**.
3. The template starts from ws-01, named ws-01. Change **Template Name**
   to `Engineering workstation`, and add a **Template Description**, for
   example `CORP workstation with CAD tools`.
4. Select **Save to diagram**.

The template now shows under **This diagram** in **Templates**. Saving it is
one change of the diagram. **Undo** removes the template, with the name
"Added template Engineering workstation".

A template made from a selected device keeps all of the device except its
connections. The VLAN of each interface is emptied, because a VLAN says what
the device is connected to in this diagram. Builder first saves the changes
that the Inspector holds unapplied for that device. When no device is
selected, or several nodes are selected, the template starts from a plain
device (hostname `device`, image `ubuntu.qc2`) with no name.

In a draft that you can only view, the **+** does nothing, and its tooltip
says "This diagram is read only." When the diagram has the most templates
it can hold, the tooltip says "This diagram has 50 templates, the most it
can hold."

### The template editor

![The New device template editor made from ws-01: on the left, Template Name Engineering workstation and Template Description CORP workstation with CAD tools with its hint; on the right, under Node fields in two columns, Hostname ws-01, Icon Windows, Icon size Diagram default (Small), Outline Color, Fill Color, Layer None with its hint, and the Node section with Type VirtualMachine and the first General fields, Description, Do not boot, Node hostname and Notes, where the scroll area ends; and Cancel and Save to diagram at the bottom.](../images/builder/template-editor.png)

On the left are the fields of the template:

- **Template Name** (required).
- **Template Description**. Its hint says "The Nodes & Templates section
  shows this text as a tooltip. The template does not copy it to the node."

For their lengths, see [Limits](#limits). A name or a description is saved
on one line: a pasted line break or tab becomes a space. A name that another
template already has shows "Another template has this name." under
**Template Name**, but it is allowed.

On the right, **Node fields** has the fields of the Inspector, in columns:
**Hostname**, **Icon**, **Icon size**, **Outline Color**, **Fill Color**,
**Layer** and the node spec. **Icon** also chooses a custom icon (see
[The Icon field](editor.md#the-icon-field)). **Layer** comes directly
after the colors, above the node spec. For a template of the diagram, it
offers the layers of the diagram. For a library template, it offers the
layers of the default layer set, or the placeholder layers when the server
has no default set (see [The Layers tab](drafts.md#the-layers-tab)). When the
template has no layer of its own, **Layer** shows the layer that its fields
match with the node matchers of those layers (see
[Layers](diagrams.md#layers)). The template editor has no **Edit layers…**.
The rarely used fields of the node spec are in **More settings** at the
bottom, a bar that you select to open. The fields have the same checks as
on the canvas (see [Editing a node](editor.md#editing-a-node)).
**Hostname** is the name that new devices start from. When **Icon size** is
**Diagram default**, the device draws its icon at the size of the diagram
that it is in. For a template of the diagram, the choice names the size of
that diagram, for example "Diagram default (Large)". For a library template,
the choice names no size, because the device can go into any diagram. There
is no **Apply**, no **Position** and no **Connection points**. **Save** keeps
everything.

**Save** (or **Save to diagram** for a new template) first checks the
fields. When a field has an error, focus moves to the list of fields that
need attention, for example "1 field needs attention before you can save
this template." A template that is too large says "This template is too
large to save (16 KiB at most). Remove some of its settings."
<kbd>Enter</kbd> in **Template Name** or **Template Description** saves.

**Cancel**, <kbd>Esc</kbd>, the close button or a click outside the dialog
closes it. If you made changes, it asks first: "Discard changes to this
template?", with **Keep editing** and **Discard**.

### Editing and deleting a diagram template

A template of **This diagram** has a menu beside it ("Actions for template"
and its name):

- **Edit** opens the template editor, titled "Edit template" and the name.
- **Update**, on a linked template that has a newer version (see
  [Linked templates](#linked-templates)).
- **Save to library** adds a copy of the template to the library: "Saved
  Engineering workstation to the library." The template of the diagram then becomes a linked template of
  the new library template. This link is one change of the diagram, "Linked
  template Engineering workstation to the library". When the diagram carries
  its own copy of the custom icon, that copy goes to the icon library of the
  server first (see
  [The diagram and its icons](diagrams.md#the-diagram-and-its-icons)). If
  that fails, Builder saves the template and says so, for example "The
  server already has an icon named plc-icon that differs from this
  diagram's. The saved template shows the server's icon."
- **Unlink**, on a linked template (see [Linked templates](#linked-templates)).
- **Delete** removes the template from the diagram, without a question. It
  is one change: **Undo** brings it back.

## Linked templates

A linked template is a template of the diagram that is a copy of a library
template or of a server template. The diagram gets a linked template in two
ways:

- You add a device from a library template or a server template (see
  [Templates in the editor](#templates-in-the-editor)).
- You select **Save to library** in the menu of a diagram template.

The link names the template that the copy came from, and the version that
the diagram has. Builder compares each linked template with the library when
a draft opens, and each time it reads the library. When the linked template
needs your attention, a mark shows under it in **Templates**:

| Mark | What it means | Button on the mark |
|---|---|---|
| **Update available** | The library has a newer version of the template. For a server template, an administrator changed the template file and restarted the server. | **Update** |
| **Deleted from library** | A user deleted the library template. | **Unlink** |
| **Removed from server** | The server collection no longer has the template. | **Unlink** |

The tooltip of the template gives the reason in full, for example "The
library has a newer version of this template (version 3). The diagram has
version 2." or "The library template was deleted." A draft that you can
only view shows the marks without buttons.

![Templates with the section This diagram (2): Workstation and Server, each with its menu button, and under each the mark Update available with an Update button.](../images/builder/palette-template-update.png){ width="240" }

With nothing selected, the Inspector shows the same templates under
**Template updates** (see [With nothing selected](editor.md#with-nothing-selected)).
The notice says how many templates have a newer version and how many lost
their template, and "Devices made from these templates do not change." Each
template has **Update** or **Unlink**. When two or more templates have a
newer version, **Update all** updates all of them.

![The Template updates notice in the Inspector: The library has a newer version of 2 templates in this diagram. Devices made from these templates do not change. Below it, Workstation and Server, each with The library has a newer version of this template (version 2). The diagram has version 1. and an Update button, and at the bottom Update all.](../images/builder/inspector-template-updates.png){ width="352" }

To update a linked template:

1. Select **Update** on its mark, in its menu, or in the Inspector.
2. Builder asks, for example "Update template PLC?": "The template in this
   diagram gets version 3 from the library. Devices made from the template
   do not change. You can undo this." When you changed the template in the
   diagram after Builder copied it, the question adds "This template was
   changed in the diagram. The update replaces these changes."
3. Select **Update template**.

The template of the diagram gets the name, the description and the device
fields of the library template. The update is one change, for example
"Updated template PLC from the library". **Update all** asks once, for
example "Update 2 templates?", and its button is **Update templates**.

**Unlink** removes the link and keeps the template as it is, without a
question. It is one change, for example "Unlinked template PLC from the
library". Builder then no longer compares the template with the library.

You can change a linked template in the diagram. It keeps its link, and an
update replaces your changes.

## The template library

The phenix server keeps one template library for all of its users. No user
owns it. Each user who can use the Builder sees the same templates and
collections, under **Templates** in the editor and on the **Node Templates**
tab. A change that one user makes shows for all users the next time Builder
reads the library. A linked template of a diagram then shows **Update
available** (see [Linked templates](#linked-templates)).

Each Builder user can add, change and delete the templates and the
collections of the library (see
[The libraries](administration.md#the-libraries)). Only the built-in
templates and the server collections are read only.

### The Node Templates tab

To see the library, select the **Node Templates** tab on the drafts page,
or select **Node Templates library** beside **Templates** in the editor. The
command palette has **Open Node Templates library** in the editor, and
**Show Node Templates** on the drafts page. In the command palette, type
`c:` and part of a collection name to show that collection, or `t:` and part
of a template name to open that template in the template editor.

![The Node Templates tab showing the collection Riverside lab: New template, New collection and Import templates; Show set to Riverside lab; the collection's name, 5 templates, Updated Oct 10, 2026, 5:47 AM by e2e-admin, its description, and Edit collection, Delete collection and Export collection; the selection row with Select all, 2 of 5 selected, Add to collection, Remove from collection, Delete selected and Export selected; and cards for Server, Workstation, Router, Firewall and External device, with Workstation and Firewall selected, each saying Updated Oct 10, 2026, 5:47 AM by e2e-admin and In Riverside lab, with its description and Edit, Delete and Export.](../images/builder/node-templates-tab.png)

The tab holds:

- **New template**, **New collection** and **Import templates** (see
  [Exporting and importing templates](#exporting-and-importing-templates)).
- **Show**, which selects the list: **All library templates**, the
  collections of the library under **Library collections** (when there are
  any), **Built-in templates**, and the server collections under **Server**.
- A row for selecting several templates (see
  [Selecting several templates](#selecting-several-templates)).
- A card for each template. The card shows the icon and name of the
  template, a checkbox at the top right, and when the template changed last
  and who changed it, for example "Updated Oct 15, 2026, 9:12 AM by alice".
  It also shows the collections that the template is in ("In" and their
  names), its description, and **Edit** (or **View**), **Delete** and
  **Export**.

The cards of the built-in templates and of the server collections have the
tag **Read only**, and the buttons **View**, **Duplicate** and **Export**
(see [Built-in templates](#built-in-templates) and
[Server collections](#server-collections)).

Until the first read is complete, the tab says "Loading…". When Builder
cannot read the library, the tab says "Could not load the template
library." and why, with **Retry**. An empty library says "The library has no
templates. Select New template to make one."

**New template** opens the template editor, titled **New library
template**, on a plain device with no name. The **Edit** button of a card
opens the editor on that template. **Save to library** (or **Save**) sends
the template to the server. **Undo** cannot reverse this change. When the
save fails, the editor stays open and gives the reason, for example "Could
not save the template. A library holds at most 200 templates." When another
user, or you in another tab or window, changed the template after you opened
it, the editor says "This template changed after you opened it, by another
user or in another window. To replace that version, select Save again. To
keep it, select Cancel and open the template again."

**Delete** asks first, for example "Delete template Engineering
workstation?": "It is removed from the library and its collections, for all
users. Diagrams keep their copies of it. You cannot undo this." Select
**Delete template**. A linked template of the deleted template then shows
**Deleted from library** in each diagram.

### Built-in templates

phenix has five built-in templates: **Server**, **Workstation**,
**Router**, **Firewall** and **External device** (see
[Adding devices](diagrams.md#adding-devices)). They are read only for
everyone, administrators included. Every diagram lists them under
**Built-in**. A device made from a built-in template adds no template to the
diagram.

To see them on the **Node Templates** tab, choose **Built-in templates** in
**Show**. The list says "Built-in templates are read only. To change one,
select Duplicate. This adds a copy to the library." Each card has the tag
**Read only**, and these buttons:

- **View** opens the template editor read only, titled "Template" and the
  name, with "A built-in template, which is read only" under the title.
  **Duplicate to library** adds a copy to the library. **Close** closes the
  editor.
- **Duplicate** adds a copy to the library, for example "Added a copy of
  Router to the library." Change the copy in the library.
- **Export** saves the template as a file (see
  [Exporting](#exporting)).

### Collections

A collection is a named group of library templates. A template can be in
several collections. All users see the same collections.

1. Select **New collection**. The **New collection** dialog asks for a
   **Name** (required) and a **Description**.
2. Select **Create collection**.

To fill it, select templates under **All library templates**, open **Add to
collection** in the selection row, and choose the collection. **New
collection…** at the end of that menu makes a new collection with the
selected templates in it.

Choose a collection in **Show** to list its templates. Above them are the
name of the collection, how many templates it holds, when it changed last
and who changed it, its description, and **Edit collection**, **Delete
collection** and **Export collection**. While a collection is shown,
**Remove from collection** removes the selected templates from it. **Edit
collection** changes only the name and the description.

**Delete collection** asks first, for example "Delete collection Pumps?":
"The collection is removed for all users. Its templates stay in the
library." Select **Delete collection**. When you delete a template, it is
removed from its collections.

### Selecting several templates

Each card has a checkbox at its top right. The row above the cards has
**Select all**, how many are selected ("2 of 5 selected"), and the
actions. For a list of the library:

- **Add to collection** and, while a collection is shown, **Remove from
  collection**.
- **Delete selected**, which asks once: "Delete 2 templates?". Select
  **Delete templates**. Devices already made from them do not change.
- **Export selected** (see
  [Exporting and importing templates](#exporting-and-importing-templates)).

For the built-in templates and a server collection, the row has
**Duplicate selected**, which adds a copy of each selected template to the
library, and **Export selected**.

When you choose another list in **Show**, the selection is cleared. The keys
of the drafts page also select cards (see
[Selecting with the keyboard](drafts.md#selecting-with-the-keyboard)).

## Exporting and importing templates

A template file holds one collection of templates, as YAML or JSON. Export
writes the same format that Import and the phenix server read. You can
import an exported file on another phenix server, or put it in the template
directory of a server as it is (see [Server collections](#server-collections)).

### Exporting

**Export** saves a YAML file:

- On a card, **Export** saves the one template, as a collection with the
  name of the template.
- In the selection row, **Export selected** saves the selected templates,
  as a collection named after the list shown (the name of a collection, or
  for example "Library").
- On a collection that you show, **Export collection** saves the collection
  with its name and description.

The file is named after the collection or template, for example
`plant-floor.templates.yaml`. Templates from all sources export: the
library, the built-in templates and the server collections. The file carries
a copy of each custom icon that its templates name, from the icon library of
the server. Builder says what it saved, for example "Exported 2 templates to
plant-floor.templates.yaml." It also names each icon that the icon library
does not have. The file does not carry that icon.

The templates of a file need names that are different when case is
ignored. A library can hold two names that are not, for example "PLC" and
"plc". Export keeps the first name and adds a number to the others, "plc
(2)", "plc (3)" and so on. It says so: "The templates of a file need names
that differ even ignoring case, so it holds "plc" as "plc (2)"."

A file looks like this (the example file
[node-templates.yaml](examples/node-templates.yaml) is another one, with
three templates and no icons):

```yaml
$schema: https://phenix.sandia.gov/schemas/builder/templates/v1
name: Plant floor
description: Devices of the plant's control network.
templates:
  - name: PLC
    description: Programmable logic controller
    device:
      iconKey: server
      icon: plc-icon
      spec:
        type: VirtualMachine
        general:
          hostname: plc
          vm_type: kvm
        hardware:
          os_type: linux
          drives:
            - image: ubuntu.qc2
icons:
  plc-icon:
    data: iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==
```

- `$schema` (required) names the format. The server serves its JSON
  Schema at `/api/v1/schemas/builder/templates/v1`.
- `name` (required) is the name of the collection, and `description` is
  its description. Each is one line (see [Limits](#limits)).
- `templates` (required) holds the templates. Each has a `name`, an
  optional `description`, and a `device` with the fields that the template
  fills in, as a library template has them. No two names can be different
  only in case. Templates have no IDs. The place where a template is kept
  gives it an ID.
- `icons` (optional) holds copies of custom icons, by icon name. Each is a
  PNG in base64, as a downloaded diagram carries them (see
  [Custom icons](diagrams.md#custom-icons)). A template can also name an
  icon that the file does not carry. The icon library of the server must
  then hold that icon.

YAML anchors and aliases (`&name`, `*name`) are not allowed. For the size
limits of a file, see [Limits](#limits).

### Importing

To import a template file into the library:

1. Select **Import templates** on the **Node Templates** tab.
2. Choose the file. Builder reads it immediately. For a file that it cannot
   import, it gives each problem and its location in the file, for example
   `templates[1].name: template name "plc" is also the name of templates[0],
   ignoring case`. A file it can import is described, for example "Plant
   floor: 2 templates and 1 custom icon."
3. Select **Import**.

The dialog in step 3, with the example file
[node-templates.yaml](examples/node-templates.yaml):

![The Import templates dialog with node-templates.yaml chosen as the Template file, its hint, the description Example Plant Templates: 3 templates., and the Cancel and Import buttons.](../images/builder/template-import.png){ width="592" }

The templates are added to the library as copies, in a new collection with
the name and description of the file. All users see them. When a collection
of the library already has that name, the new collection gets a number. The
dialog says so before you import, for example "The library has a collection
of that name, so Import adds it as Plant floor (2)." Builder then says
"Imported 2 templates as collection Plant floor (2)."

The custom icons in the file go to the icon library of the server first, as
when you upload a diagram (see
[The diagram and its icons](diagrams.md#the-diagram-and-its-icons)). If an
icon cannot go there, the import continues, and the dialog lists what
happened, for example "The server already has an icon named plc-icon that
differs from this file's. The imported templates show the server's icon."
Select **Close**.

Importing needs space in the library (see
[Limits](#limits)). When the file has too many templates, the dialog says,
for example, "The library can hold 200 templates, and this file would put
more than 200 in it. Delete some first."

## Server collections

An administrator can give everyone on a phenix server the same templates:
each template file in the server's template directory becomes one
collection, for example "NLR Node Templates" or "Sandia Node Templates".
phenix reads the directory when it starts (see
[Template files on the server](administration.md#template-files-on-the-server)).

Server collections show:

- Under **Templates** in the editor, each in a section named **Server:** and
  its name, for example **Server: NLR Node Templates**.
- On the **Node Templates** tab, under **Server** in **Show**, each by its
  name.

They are read only for everyone, administrators included. A card has the
tag **Read only**, and **View**, **Duplicate** and **Export**. A collection
has **Duplicate collection** and **Export collection**. **Duplicate
collection** adds the collection to the library, with copies of its
templates: "Added a copy of collection Example Plant Templates to the
library." To change a template, duplicate it and change the copy in the
library. The collection says "Read from a template file on the phenix
server. To change a template, select Duplicate. This adds a copy to the
library."

When you add a device from a server template, the diagram gets a linked
template of it. When an administrator changes the template file and
restarts the server, that linked template shows **Update available** (see
[Linked templates](#linked-templates)).

Here the server's template directory holds the example file
[node-templates.yaml](examples/node-templates.yaml):

![The Node Templates tab with Show set to the server collection Example Plant Templates: its name with the tag Read only, 3 templates, the note Read from a template file on the phenix server. To change a template, select Duplicate. This adds a copy to the library., its description, Duplicate collection and Export collection; the selection row with Select all, 0 of 3 selected, Duplicate selected and Export selected; and cards for PLC, HMI and Plant router, each with the tag Read only and View, Duplicate and Export.](../images/builder/templates-server.png)

## Limits

| What | Limit |
|---|---|
| Templates in a diagram | 50 |
| Templates in the library | 200 |
| Collections in the library | 50 |
| Templates in a collection | 200 |
| Whole library: templates and collections | 512 KiB |
| Template name | 1 to 128 bytes, one line |
| Template description | 1024 bytes, one line |
| Collection name | 1 to 128 bytes, one line |
| Collection description | 1024 bytes, one line |
| A template's device, as JSON | 16 KiB |
| Template file | 8 MiB, 1 to 200 templates, 50 custom icons |
| Template files the server reads | 50 |

## From a script

Scripts use the REST API to list, add, change and delete the templates and
collections of the library (see [REST API](administration.md#rest-api)).
Each template and collection in the list names the user who added it
(`createdBy`) and the user who last changed it (`updatedBy`). The command
palette finds them by these names too ("Created by" and "Updated by"). The
server collections come from the template directory, which
`--base-dir.builder-templates` names (see
[Template files on the server](administration.md#template-files-on-the-server)).
Diagram templates are part of the Builder document, so a Builder file can
hold them. The link of a linked template is its `source` (see
[Builder JSON and YAML](import-upload-download.md#builder-json-and-yaml)).
Publishing never writes diagram templates into a Topology config.
