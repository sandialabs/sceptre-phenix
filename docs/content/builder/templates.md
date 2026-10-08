# Node Templates

A device template is a set of fields filled in ahead, for a new device: its
icon, custom icon, outline and fill colors, and its whole node spec (type,
hardware, interfaces and the rest). **Add nodes** lists the templates under
**Device templates**: select one, or drag it onto the canvas, to add a
device made from it (see [Adding devices](diagrams.md#adding-devices)).

Templates are kept in two places:

- **In a diagram.** A diagram template is part of the Builder document, so
  it goes wherever the diagram goes: other tabs, the people you share the
  draft with, its downloads and its publications. See
  [Diagram templates](#diagram-templates).
- **In your library.** Each user has one template library on the phenix
  server. Its templates show in every diagram you open, and you manage them
  on the **Node Templates** tab of the drafts page. You can share them with
  other users, and publish them for everyone on the server. See
  [The template library](#the-template-library).

A template's description is only its tooltip in **Add nodes**: a device made
from a template gets no description. A device keeps no link to the template
it was made from: changing or deleting a template later changes no device.

## Templates in Add nodes

![Add nodes with Device, Switch, Note and Group, then the Device templates heading with the New device template (+) and Node Templates library buttons; under This diagram, the template Engineering workstation with its menu open, offering Edit, Save to library and Delete; and under My library, Server, Workstation, Router, Firewall and External device.](../images/builder/palette-templates.png) { width="240" }

**Add nodes** lists the device templates in groups, in this order:

| Group | Templates |
|---|---|
| **This diagram** | The templates saved in the diagram. |
| **My library** | Your own templates. A new library holds the five built-in templates. |
| **Shared with me** | Templates other users share with you. |
| **Server-wide** | Templates published for everyone on the server. |
| **Built-in** | The five built-in templates, only while your library cannot be loaded. |

A group's name shows only when two groups or more have templates, so a
diagram without templates of its own looks as it always did. Point at a
template to see its description. A template of another user adds "Shared
by" or "Published by" and the owner's name, for example "Shared by alice."

Two buttons follow the **Device templates** heading:

- **New device template** (the **+**) makes a template of this diagram (see
  [Making a diagram template](#making-a-diagram-template)).
- **Node Templates library** leaves the draft, as **Back to drafts** does,
  and shows the **Node Templates** tab of the drafts page. It works in a
  draft you can only view too.

A device made from a template:

- is named after the template's hostname, with a number when that name is
  taken, for example plc and then plc-2;
- has a connection point for each interface the template names, and is
  connected to nothing: a VLAN that names a network of the diagram is
  emptied, and other VLAN text is kept;
- brings the template's custom icon into the diagram (see
  [Custom icons](diagrams.md#custom-icons)).

In the command palette, **Add device** lists **Device**, then every template
by group, with its group and image on the second line, for example "My
library · ubuntu.qc2" or "This diagram · no image".

### When the library cannot be loaded

While your library is read for the first time, **Add nodes** says "Loading
your templates…". When it cannot be read, the five built-in templates stand
in for it under **Built-in**, with "Your library could not be loaded." and
**Retry**, so you can still build a diagram. When the read succeeds, your
library's templates replace them. A library read before stays listed while a
later read fails.

When the phenix server holds a library it cannot read (a newer version of
phenix saved it, for example), **Add nodes** shows the built-in templates and
"Your library cannot be read. A newer version of phenix may have saved it."
with no **Retry**. The **Node Templates** tab says the same, and still lists
other users' shared and server-wide templates. Open the library with the
phenix version that saved it.

Builder reads your library when it opens, with each new read of the drafts
lists, when a draft opens in the editor, when the browser window comes back
to the front while the editor is open, after you sign in again, and after
each change you make to the library.

## Diagram templates

### Making a diagram template

To save ws-01 of Riverside Water as a template of the diagram:

1. Select ws-01 on the canvas.
2. Select **New device template** (the **+** beside **Device templates**),
   or run **New device template** from the command palette. The template
   editor opens, titled **New device template**.
3. The template starts from ws-01, named ws-01. Change **Name** to
   `Engineering workstation`, and add a **Description**, for example
   `CORP workstation with CAD tools`.
4. Select **Save to diagram**.

The template now shows under **This diagram** in **Add nodes**. Saving it is
one change of the diagram: **Undo** removes it again, with the name "Added
template Engineering workstation".

A template made from a selected device keeps all of it but its connections:
each interface's VLAN is emptied, since a VLAN says what the device is
connected to in this diagram. Changes the Inspector holds unapplied for that
device are saved first. With no device selected, or several nodes, the
template starts from a plain device (hostname `device`, image `ubuntu.qc2`)
with no name.

In a draft you can only view, the **+** does nothing, and its tooltip says
"This diagram is read only." A diagram holds at most 50 templates; at 50 the
tooltip says "This diagram has 50 templates, the most it can hold."

### The template editor

![The New device template editor made from ws-01: on the left, under Template, Name Engineering workstation and Description CORP workstation with CAD tools with its hint; on the right, under Node fields in two columns, Hostname ws-01, Icon windows, Custom icon None, Outline Color, Fill Color and the Node section with Type VirtualMachine and the General fields; and Cancel and Save to diagram at the bottom.](../images/builder/template-editor.png)

The template editor is a large dialog, so the fields have room. On the left,
under **Template**:

- **Name** (required), at most 128 bytes.
- **Description**, at most 1024 bytes. Its hint says "Shown as a tooltip in
  Add nodes. It is not written to the node."

A name or a description is saved on one line: a pasted line break or tab
becomes a space. A name another template already has shows "Another template
has this name." under **Name**, but is allowed.

On the right, **Node fields** has the fields of the Inspector, laid out in
columns: **Hostname**, **Icon**, **Custom icon**, **Outline Color**, **Fill
Color** and the node spec. They have the same checks as on the canvas (see
[Editing a node](editor.md#editing-a-node)). **Hostname** is the name new
devices start from. There is no **Apply**, no **Position** and no
**Connection points**: **Save** keeps everything.

**Save** (or **Save to diagram** for a new template) first checks the
fields. When a field has an error, focus moves to the list of fields that
need attention, for example "1 field needs attention before this template
can be saved." A template's device takes at most 16 KiB as JSON; a larger
one says "This template is too large to save (16 KiB at most). Remove some
of its settings." <kbd>Enter</kbd> in **Name** or **Description** saves.

**Cancel**, <kbd>Esc</kbd>, the close button or a click outside the dialog
closes it. With changes, it asks first: "Discard changes to this template?",
with **Keep editing** and **Discard**.

### Editing and deleting a diagram template

A template of **This diagram** has a menu beside it ("Actions for template"
and its name):

- **Edit** opens the template editor, titled "Edit template" and the name.
- **Save to library** copies the template, and its custom icon, into your
  library: "Saved Engineering workstation to your library." The two are
  separate from then on. Your role needs `configs` `create`.
- **Delete** removes the template from the diagram, without asking. It is
  one change: **Undo** brings it back.

## The template library

Each user has one template library, kept on the phenix server under their
user name. It starts with the five built-in templates (**Server**,
**Workstation**, **Router**, **Firewall** and **External device**, see
[Adding devices](diagrams.md#adding-devices)). They are ordinary templates:
you can change and delete them. A deleted built-in template does not come
back; there is no way to restore it. Another user, or a new phenix server,
starts with all five.

### The Node Templates tab

To see your library, select the **Node Templates** tab on the drafts page,
or select **Node Templates library** in **Add nodes**. The command palette
has **Open Node Templates library** in the editor, and **Show Node
Templates** on the drafts page.

![The Node Templates tab showing the collection Riverside lab: New template and New collection; Show set to Riverside lab; the collection's name, 5 templates, its description, and Edit collection, Share and Delete collection; the selection row with Select all, 2 of 5 selected, Add to collection, Remove from collection, Share selected and Delete selected; and cards for Server, Workstation, Router, Firewall and External device, with Workstation and Firewall selected, each saying In Riverside lab, with its description and Edit, Share and Delete.](../images/builder/node-templates-tab.png)

The tab holds:

- **New template** and **New collection**.
- **Show**, which chooses the list: **My templates**, one of your
  collections, and, when there are any, **Shared with me** and
  **Server-wide** with their collections.
- A row for selecting several templates (see
  [Selecting several templates](#selecting-several-templates)).
- A card for each template, with a checkbox, the template's icon and name,
  when it was last updated (a built-in template you never changed has no
  time), the collections it is in ("In" and their names), who it is shared
  with, its description, and **Edit**, **Share** and **Delete**.

Until the first read answers, the tab says "Loading…". When the library
cannot be read, the tab says "Could not load your templates." and why, with
**Retry**. An empty library says "Your library has no templates. Select New
template to make one."

**New template** opens the template editor, titled **New library
template**, on a plain device with no name. A card's **Edit** opens it on
that template. **Save to library** (or **Save**) sends the template to the
server, and says "Saving…" while it does; the change cannot be undone with
**Undo**. When it fails, the editor stays open and says why, for example
"Could not save the template. A library holds at most 200 templates." When
someone changed the template in another tab or window since you opened it,
the editor says "This template was changed in another tab or window. Save
again to replace that version, or Cancel to keep it."

**Delete** asks first, for example "Delete template Engineering
workstation?": "It is removed from your library and its collections.
Diagrams that used it are not changed. This cannot be undone." Select
**Delete template**. When the template is shared, the question also names
who loses it.

### Collections

A collection is a named group of your own templates. A template can be in
several collections. Collections make it easy to share or publish several
templates at once.

1. Select **New collection**. The **New collection** dialog asks for a
   **Name** (required) and a **Description**.
2. Select **Create collection**.

To fill it, select templates under **My templates**, open **Add to
collection** in the selection row, and choose the collection. **New
collection…** at the end of that menu makes a new collection with the
selected templates in it.

Choose a collection in **Show** to list its templates. Above them are its
name, how many templates it holds, its description, and **Edit
collection**, **Share** and **Delete collection**. While a collection is
shown, **Remove from collection** takes the selected templates out of it.
**Edit collection** changes the name and the description only.

Deleting a collection keeps its templates in your library: "The collection
is removed. Its templates stay in your library." Deleting a template takes
it out of its collections.

### Selecting several templates

Each card has a checkbox. The row above the cards has **Select all**, how
many are selected ("2 of 5 selected"), and the actions your role allows:

- **Add to collection** and, while a collection is shown, **Remove from
  collection**.
- **Share selected** (see [Sharing templates](#sharing-templates)).
- **Delete selected**, which asks once: "Delete 2 templates?". Select
  **Delete templates**. Devices already made from them do not change.

Choosing another list in **Show** clears the selection.

## Sharing templates

You can share your templates and collections with other phenix users, and
publish them for everyone on the server (see
[Publishing server-wide](#publishing-server-wide)). Sharing with people
needs user sign-in and a role with `configs` `update`.

1. Select **Share** on a card, on a collection you show, or **Share
   selected** in the selection row. The **Share** dialog opens, titled for
   example "Share Engineering workstation" or "Share 2 templates".
2. In **User**, type part of a name, choose the person, and select **Add**.
   They are listed under **To add**.
3. Select **Save**.

The dialog says what the people you add can do: "People you add find this
under Shared with me in their Node Templates and in Add nodes. They can use
it and copy it, and they see your later changes. They cannot change it."

For one item, **People with access** lists you as the owner and everyone it
is shared with. **Remove** marks a person for removal, and **Keep** undoes
that. For several items, the dialog only adds people: "Adds these people to
every selected item. People who already have access keep it." Nothing
changes until **Save**; **Cancel** with changes asks first.

Sharing a collection shares the templates in it. A template or a collection
can be shared with at most 25 people. When phenix has no user sign-in, there
is only one user, so the Node Templates tab offers no **Share**, as for
drafts.

A share belongs to the person's account. When that account is deleted, or
deleted and made again under the same name, the share gives nothing, and
your Share dialog lists the person as "Account removed", to be removed when
you save. Adding the name again shares the item with the new account.

### Templates shared with you

Templates and collections that others share with you show:

- in **Add nodes**, under **Shared with me**;
- on the **Node Templates** tab, when you choose **Shared with me** in
  **Show**, or one of the shared collections, listed as its name and its
  owner's, for example "Field devices (alice)".

They are read only, and live: you see the owner's changes the next time
Builder reads your library. Each card shows the owner, and has **View** and
**Copy to my library**:

- **View** opens the template editor read only, titled "Template" and the
  name, with "Shared by alice" under the title, and **Close**.
- **Copy to my library** adds a copy that you own, for example "Copied Field
  PLC to your library." A shared collection has **Copy collection to my
  library**, which copies the collection with copies of its templates. Your
  role needs `configs` `create`.

A copy is yours: the owner's later changes do not reach it. When your
library already holds 200 templates, Builder says "Your library holds 200
templates, the most it can. Delete some first."

## Publishing server-wide

A template or a collection published server-wide is offered to everyone on
the phenix server who can use the Builder: everyone whose role has
`configs` `list` lists it under **Server-wide**, in **Add nodes** and on
the **Node Templates** tab. Publishing a collection publishes its templates.
A published item has the tag **Server-wide** on its card.

To publish, your role needs `configs` `update` and the
`builder-templates` `publish` permission, and the item must be your own.
The built-in **Builder** role has that permission, and so has the Global
Admin role (see [Permissions](administration.md#permissions)). With it, the
Share dialog has a **Server-wide** part:

- For one item, the checkbox **Published server-wide: anyone who can use the
  Builder on this server can use it**.
- For several items, the choice **Leave as it is**, **Publish
  server-wide** or **Remove from server-wide**.

Select **Save**. Builder says, for example, "Published Engineering
workstation server-wide."

To take an item back, clear the checkbox in its Share dialog, or choose
**Remove from server-wide**. A role with `builder-templates` `publish` can
also take back another user's item: show **Server-wide** (or the item's
collection), select the item, and select **Remove from server-wide**. Builder
asks first, for example "Remove Field PLC from server-wide?": "It stays in
alice's library. Other people can no longer use it unless it is shared with
them." Select **Remove**. A template that is server-wide only through its
collection goes back with the collection.

With phenix's user sign-in turned off, there is one library, of the one
user everyone is, and it can publish server-wide.

## Permissions

The template library uses the `configs` permission of the verb that matches
the action, on any name:

| Action | Permission |
|---|---|
| See the library, and the shared and server-wide templates | `configs` `list` |
| **New template**, **New collection**, **Save to library**, **Copy to my library** | `configs` `create` |
| **Edit**, **Edit collection**, **Add to collection**, **Remove from collection**, **Share** | `configs` `update` |
| **Delete**, **Delete collection**, **Delete selected** | `configs` `delete` |
| Publish server-wide, and take back any user's server-wide item | `builder-templates` `publish`, with `configs` `update` |

A role without `configs` `create` sees "Your role can view templates, but
not create them." Your library is your own: no role, Global Admin included,
reads or changes another user's library, except to take back a server-wide
item. See [Administration](administration.md) for the roles and the
storage.

## Limits

| What | Limit |
|---|---|
| Templates in a diagram | 50 |
| Templates in a library | 200 |
| Collections in a library | 50 |
| Templates in a collection | 200 |
| Custom icons used by a library's templates | 32 |
| Whole library: templates, collections and icons | 512 KiB |
| Template name | 1 to 128 bytes, one line |
| Template description | 1024 bytes, one line |
| A template's device, as JSON | 16 KiB |
| People a template or a collection is shared with | 25 |

A library is kept by user name, as drafts are: deleting a user's account
does not delete their library, and a new account of the same name owns it.

## From the command line

There is no `phenix` command for the template library: use the
**Node Templates** tab or the REST API (see [REST API](administration.md#rest-api)).
Diagram templates are part of the Builder document, so a Builder file that
`phenix builder publish` reads may hold them; publishing never writes them
into a Topology config.
