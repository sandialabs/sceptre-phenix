# Drafts

Every diagram in Builder v2 is a draft. A draft lives on the phenix server,
apart from the phenix configs. Builder v2 saves each change to the draft as
you make it. Only **Publish** writes Topology, Scenario and Experiment configs
(see [Publishing](publishing.md)).

## The drafts page

Select **Builder v2** in the phenix navigation bar. The drafts page opens.

![The Builder v2 drafts page with the Blank diagram, Import, Upload and Commands buttons, the My Drafts, Shared with me and Published Diagrams tabs, and cards for Metro Campus, Riverside Water and Riverside Water expansion.](../images/builder-v2/drafts-page.png)

The buttons at the top right are:

- **Blank diagram**: starts a new, empty draft.
- **Import**: starts a draft from a Topology or Experiment config (see
  [Import](#import)).
- **Upload**: starts a draft from a Builder document (see
  [Upload](#upload)).
- **Commands**: opens the command palette (<kbd>⌘</kbd>+<kbd>K</kbd> on
  macOS, <kbd>Ctrl</kbd>+<kbd>K</kbd> on Windows and Linux). See
  [Command palette](editor.md#command-palette).
- The theme, **Settings**, **Help** and **Focus mode** buttons, as in the
  editor (see [Header](editor.md#header)). **Help** opens this Builder v2
  documentation in a new tab.

The tabs below them list what you can open. Each tab shows how many items it
holds, for example **My Drafts (3)**.

- **My Drafts**: the drafts you own.
- **Shared with me**: drafts other users shared with you, the most recently
  changed first (see [Sharing a draft](#sharing-a-draft)).
- **Published Diagrams**: the topologies that have a Builder v2 diagram
  (see [Published diagrams](#published-diagrams)).
- **Other users' drafts**: other users' drafts that your role lets you see.
  This tab appears only when your role has the `builder-drafts` permission
  and there is such a draft (see
  [Permissions](administration.md#permissions)).

Each draft is a card with:

- The draft's name.
- The owner and when the draft last changed, for example
  "Owner: e2e-admin · Updated Sep 29, 2026, 12:52 PM". When someone you
  shared the draft with made the last change, the card adds their name:
  "Updated Sep 29, 2026, 1:02 PM by alice".
- On your own shared drafts, who has access: "· Shared with alice and bob".
- On other users' drafts, what you may do: "· Can view" or "· Can edit".
- **Open**, and for your own drafts **Share** (when phenix has user sign-in)
  and **Delete**.

"Updated" on a card is the last activity on the draft: an edit, but also an
undo, a restore, a deleted snapshot or a publish. It is not the same as
**Last edited** in the Inspector, which says when the content you see was
saved, and by whom (see
[With nothing selected](editor.md#with-nothing-selected)). After an edit
the two agree. After an undo, for example, the card says when you undid,
and the Inspector says when the version you went back to was saved.

A role without the `configs` `create` permission sees no **Blank diagram**,
**Import** or **Upload**, and the page says "Your role can open drafts and
published diagrams, but not create drafts."

## Creating a draft

### Blank diagram

Select **Blank diagram**. A new draft named "Untitled topology" opens in the
editor. When you already have a draft of that name, the new one is named
"Untitled topology 2", and so on.

To rename it, for example to `Pump station`, select **Edit diagram name**
(the pencil after the name in the header), type the name and press
<kbd>Enter</kbd> (see [Renaming the diagram](editor.md#renaming-the-diagram)).
The drafts page then lists the draft under its new name.

### Import

**Import** makes a draft from a Topology or Experiment config, stored in
phenix or uploaded from a file. The draft is named after the config, for
example riverside-water. See
[Importing a topology or experiment](import-export.md#importing-a-topology-or-experiment).

### Upload

**Upload** makes a draft from a Builder document: a Builder JSON or YAML file
that **Export** saved, pasted text, or a published diagram. Uploading always
makes a new draft. See
[Uploading a Builder document](import-export.md#uploading-a-builder-document).

### From a published diagram

Open a card on the **Published Diagrams** tab and select **Edit as a draft**
(see [Published diagrams](#published-diagrams)). Editing a Builder v2
topology on the **Configs** page does the same (see
[Editing a published topology](publishing.md#editing-a-published-topology)).
The draft starts as the published diagram, unchanged: the Inspector still
shows who made it and who edited it last, until your first change.

Builder v2 also makes a draft when it keeps your changes apart from a draft
you can no longer save to. That draft is named after the diagram, with
" (local copy)" at the end, for example "Riverside Water (local copy)" (see
[When the draft changed on the server](#when-the-draft-changed-on-the-server)).

## Opening a draft

On the drafts page, select **Open** on the draft's card. The button says
**Opening…** until the diagram shows.

You can also open a draft from the command palette:

1. Select **Commands**.
2. Type `open draft` and press <kbd>Enter</kbd>. The palette lists your
   drafts, the published diagrams and the other drafts you can open, each
   with its tab and owner, for example "Metro Campus" with
   "My Drafts · Owner: e2e-admin".
3. Type part of the name, for example `metro`, and press <kbd>Enter</kbd>.

To go back to the list, select **Back to drafts** in the editor header. It
does not wait for changes to be sent (the button says **Saving…** first only
when this browser cannot keep your changes, and **Loading…** while the lists
load, when they do not show the draft yet). Changes that are still waiting
keep being sent in the background, and the draft's card says how that goes.
For example, a draft you left while offline says "Offline: 1 change kept on
this device. Saving retries automatically.", and then "All changes saved."
once the server has the change.

### Links to a draft

A link to a draft has the form `<phenix address>/builder-v2?draft=<owner>/<draft id>`,
for example:

```text
https://phenix.example.com/builder-v2?draft=e2e-admin/968d2b25-04ec-489c-874c-2d53bfbfc8f1
```

**Copy link** in the Share dialog copies it (see
[Sharing a draft](#sharing-a-draft)). The link opens the draft for anyone who
can see it: its owner, the people it is shared with, and roles with the
`builder-drafts` permission. Opening a topology from the **Configs** page
also puts a link like this in the address bar, so a reload reopens the same
draft.

## How drafts save

### Autosave and the save state

Builder v2 saves every change to the draft on the server as you make it.
Each saved change is a snapshot in [Draft History](#draft-history). There is
no Save button. To send the changes at once, press
<kbd>⌘</kbd>+<kbd>S</kbd> on macOS or <kbd>Ctrl</kbd>+<kbd>S</kbd> on
Windows and Linux (**Save now**). **Save now** also applies the Inspector
changes you have not applied yet, when they are valid.

The save state, after **Draft History** at the end of the toolbar, says
where your changes are:

| Save state | What it means | What to do |
|---|---|---|
| All changes saved | The server has every change. | Nothing. |
| Saving changes, Saving 2 changes | Builder v2 is sending changes. | Wait. |
| 2 unsaved changes | Changes wait to be sent. | Wait, or press **Save now**. |
| Offline: 1 change kept on this device. Saving retries automatically. | The server cannot be reached. The changes are kept in this browser. | Keep working. See [Working offline](#working-offline). |
| Offline: 2 changes not stored anywhere yet. Keep this tab open; saving retries automatically. | The server cannot be reached, and the browser could not store the changes either. | Keep the tab open until the save state says All changes saved. |
| Offline: no unsaved changes | The server cannot be reached, and nothing is waiting. | Nothing. |
| 1 change not saved yet. Choose which changes to save. | Another tab of this browser also has changes to this draft. | See [One draft in several tabs](#one-draft-in-several-tabs). |
| This draft changed on the server | Someone saved a newer version first. | See [When the draft changed on the server](#when-the-draft-changed-on-the-server). |
| Not saved: you chose another tab's changes | You chose to save another tab's changes. | Builder v2 keeps this tab's changes as a new draft. |
| You cannot save changes to this draft | You lost access to the draft. | See [When access changes](#when-access-changes). |
| Not saved: your session has ended. Sign in again to save your changes. | Your sign-in expired. | See [Signing in again](#signing-in-again). |
| Could not save your last change. … Fix the diagram and it saves again. | The server refused the diagram, for example because two devices have the same hostname. The message names the problem. | Fix it (see [Checks and warnings](editor.md#checks-and-warnings)). The next change saves the diagram. |
| Could not save your changes. … Saving retries automatically. | The server refused the save for another reason, which the message names. | Wait, or select **Retry saving**. |

**Retry saving** appears after the save state when a save failed for a
reason that trying again can fix, or the browser is offline. It sends the waiting changes at once.

### Working offline

When the server cannot be reached, Builder v2 keeps your changes in this
browser and sends them when the server answers again. You can keep editing
meanwhile.

For example, open the Riverside Water expansion draft, and suppose the
network between your browser and the server goes down:

1. Under **Device templates**, select **Server**. A device named server
   appears. The save state says "Offline: 1 change kept on this device.
   Saving retries automatically.", and **Retry saving** appears.
2. When the server answers again, the save state says "All changes saved"
   within a few seconds. **Retry saving** sends the changes at once instead
   of waiting for the next try.

The save state in step 1:

![The toolbar with the save state Offline: 1 change kept on this device. Saving retries automatically.](../images/builder-v2/save-state-offline.png)

The changes stay in this browser even if you close the tab. The next time
you open the draft in this browser, Builder v2 sends them. Logging out
deletes changes that are still waiting (see [Logging out](#logging-out)).

### One draft in several tabs

You can open a draft in more than one tab of the same browser. Each tab shows
a notice under the header:

![A notice under the editor header: This draft is also open in another tab, where all changes are saved. Changes made in both tabs cannot both be kept.](../images/builder-v2/two-tabs-warning.png)

While every tab saves as it goes, nothing more happens. When more than one
tab holds changes that are not saved yet (after working offline, for
example), only one tab's changes can be saved to the draft. The notice then
says how many changes the other tab has, and **Choose which to save**
appears. Builder v2 opens the **Choose which changes to save** dialog:

1. Under **Version to save**, choose a version, for example "This tab: 1
   change, last at Sep 29, 2026, 1:01:26 PM" or "Another tab: 2 changes,
   last at Sep 29, 2026, 1:01:28 PM". **Export** next to this tab's version
   saves a copy of it as a file.
2. Select **Save this version**.

The version you chose is saved to the draft. Each other version is saved as a
new draft named after the diagram with " (local copy)", for example
"Riverside Water expansion (local copy)", and the tab that held it switches
to that draft. **Decide later** closes the dialog: nothing is sent until you
choose, and **Choose which to save** opens the dialog again.

### When the draft changed on the server

When someone else saves a newer version of the draft before your changes
reach the server, your changes cannot be saved over it. This happens, for
example, when you work offline on a draft you shared with **Can edit**, and
the other person edits it meanwhile. A panel opens under the header:

> **This draft changed on the server**
>
> alice saved a newer version of this draft, so your changes cannot be
> written over it.
>
> Your 1 unsaved change is kept on this device. Choose how to keep your work:
> edits you make now are not saved until you choose.

Choose one:

- **Save my history as a new draft**: saves your version as a new draft of
  your own, named for example "Riverside Water (local copy)", and opens it.
  The draft that changed on the server stays as it is.
- **Discard mine and load the server version**: asks "Discard your unsaved
  changes?". Select **Discard and load the server version** to delete your
  changes and load the newer version. This cannot be undone.

A role that cannot create drafts gets **Export** instead of **Save my history
as a new draft**: export a copy of your work before you load the server
version.

### When the server is out of space

When the phenix store has no room left (an etcd store at its space quota, for
example), the save state says "Could not save your changes." with the
server's reason. Your changes stay in this browser, and saving retries
automatically until the server has room again. See
[Storage](administration.md#storage).

## Draft History

Each change saved to a draft is a snapshot. **Draft History** lists them, and
restores an earlier one.

For example, in the Riverside Water draft:

1. Under **Device templates**, select **Server**.
2. In the Inspector, set **Hostname** to `files-02` and select **Apply**.
3. Select **Draft History** in the toolbar.

![The Draft History dialog with three snapshots, newest first, each with its number, name, date and user, and Restore and Delete buttons; the newest is marked Current.](../images/builder-v2/draft-history.png)

The table lists the snapshots newest first, with these columns:

- **#**: the snapshot's number. 1 is the oldest, the draft as it was made.
- **Name**: what the change did, for example "Draft created", "Added device"
  and "Updated device files-02".
- **Date** and **User**: when the snapshot was saved, and who saved it. They
  are what the Inspector shows as **Last edited** while that snapshot is the
  current one. Snapshot 1 of a draft made with **Edit as a draft** is the
  exception: its row says when you made the draft, and the Inspector says
  who last edited the diagram before that.
- The Restore and Delete buttons.

The draft's current snapshot is marked **Current**. It can be neither restored
nor deleted. Screen readers announce how many snapshots there are, for
example "3 snapshots, newest first."

### Restoring a snapshot

To go back to the draft as it was made:

1. Select the name **Draft created**, or its Restore button.
2. The dialog closes and the canvas shows the draft without files-02. The
   row of "Draft created" is now marked **Current**.

Riverside Water is now as you uploaded it, as the other pages expect.

Restoring does not delete the newer snapshots. They stay in **Draft
History**, and you can restore one of them the same way.

**Undo** and **Redo** in the toolbar move the draft's current snapshot too,
one change at a time. They reach only the changes you made since you opened
the draft in this tab, or since your last restore. To go back further, use
**Draft History**. The keys are <kbd>⌘</kbd>+<kbd>Z</kbd> and
<kbd>⇧</kbd>+<kbd>⌘</kbd>+<kbd>Z</kbd> on macOS, and
<kbd>Ctrl</kbd>+<kbd>Z</kbd> and <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>Z</kbd>
(or <kbd>Ctrl</kbd>+<kbd>Y</kbd>) on Windows and Linux.

!!! warning
    A change made after you restore or undo removes every newer snapshot. In
    the example, adding a note after restoring "Draft created" removes
    "Added device" and "Updated device files-02", and the note becomes
    snapshot 2.

### Deleting a snapshot

1. Select the Delete button of the snapshot.
2. Builder v2 asks "Delete snapshot?" and names it, for example "Draft
   created, Sep 29, 2026, 12:52:40 PM is removed from the draft history. This
   cannot be undone."
3. Select **Delete snapshot**.

**Undo** and **Redo** skip a deleted snapshot.

### Automatic snapshots

When you leave a draft, publish it or export it while the Inspector holds
changes you have not applied, Builder v2 applies and saves them for you.
Their snapshot is named for the node, for example "Saved unapplied changes to
Device web-01", and marked **Automatic**. A line under the table says
"Automatic: changes you had not applied in the Inspector, saved for you before
you left, published or exported the diagram."

### Limits

A draft keeps at most 50 snapshots and 50 MiB of them. Past either limit,
Builder v2 drops the oldest snapshots. A user who can only view the draft
sees its history without the Restore and Delete buttons. A published diagram
has no history: its **Draft History** says "A published diagram has no draft
history. Edit it as a draft to keep one." For a
[diagram read from a file](#diagrams-read-from-a-file) it says "A diagram
read from a Builder file has no draft history. Edit it as a draft to keep
one."

## Sharing a draft

You can share your own drafts with other phenix users. Sharing needs user
sign-in on the phenix server, and a role with the `configs` `update`
permission. Each person gets one of two kinds of access:

| Access | What the person can do |
|---|---|
| **Can view** | Open the draft and its history, and export it. |
| **Can edit** | Also change it, undo and redo, restore and delete snapshots, and publish it under their own permissions. |

Neither kind lets the person delete the draft or change who has access. The
person's role must also allow it: they still need `configs` `get` to open the
draft, and `configs` `update` to change it (see
[Permissions](administration.md#permissions)).

The example below shares the Riverside Water draft with carol. The example
lab has three users: alice (Alice Nguyen), bob (Bob Martin) and carol (Carol
Diaz), and Riverside Water is already shared with alice (**Can edit**) and bob
(**Can view**). Use your own users' names.

1. On the Riverside Water card, select **Share**. In the editor, **Share** is
   in the toolbar. The **Share Riverside Water** dialog opens and lists
   **People with access**: you as **Owner**, then alice and bob.
2. Select the **User** field and type `car`. The list offers
   "Carol Diaz (carol)". Choose it.
3. Under **Access**, choose **Can edit**.
4. Select **Add**. carol appears under **People with access**, marked
   **New**.
5. Select **Save**. The dialog closes, and the card says "Shared with alice,
   bob and carol".

The dialog in step 2:

![The Share Riverside Water dialog: the User field with Carol Diaz (carol) offered, Access set to Can view, and People with access listing e2e-admin as owner, alice with Can edit and bob with Can view, above the Copy link, Cancel and Save buttons.](../images/builder-v2/share-dialog.png)

In the dialog:

- The **User** list holds the users the draft can be shared with, shown as
  "Name (username)". It filters as you type, and leaves out the people
  already listed. When no one is left, it says "No other users to share
  with."
- Each person's access select changes their access. Their row is then
  marked **Changed**.
- **Remove** marks a person "Removed when you save". **Keep** undoes that.
- Nothing changes until you select **Save**. **Cancel** with changes asks,
  for example, "Discard 1 unsaved change?", with **Keep editing** and
  **Discard**.
- A draft can be shared with at most 25 people.
- **Copy link** copies the link to the draft (see
  [Links to a draft](#links-to-a-draft)) and says "Link copied.". Over plain
  HTTP the browser allows no clipboard: the dialog shows the link in a
  **Link to this draft** field, selected, and says "Press ⌘C to copy the
  link." on macOS or "Press Ctrl+C to copy the link." on Windows and Linux.
- The people you add find the draft under **Shared with me**. They see all
  of it, including its history.

### What the people you share with see

The draft appears on their **Shared with me** tab, with the owner and their
access, for example "Owner: e2e-admin · Can view · Updated Sep 29, 2026,
12:52 PM".

When bob (**Can view**) opens Riverside Water, the header says "Shared by
e2e-admin · Can view", and a notice says "e2e-admin shared this draft with
you to view. Use Export to keep a copy." The buttons that change the diagram
are unavailable. **Share** is unavailable too; its tooltip says "Only
e2e-admin can change who has access".

![Riverside Water opened by bob: the header says Shared by e2e-admin · Can view, a notice says e2e-admin shared this draft with you to view and to use Export to keep a copy, and the editing buttons are unavailable.](../images/builder-v2/shared-view-only.png)

When alice (**Can edit**) opens it, the header says "Shared by e2e-admin ·
Can edit", and she edits it as you do. Her changes go to the same draft, and
the owner's card then says "Updated … by alice".

When a draft is shared with **Can edit** but the person's role cannot change
configs, their card says "Can edit (your role allows viewing only)", and the
draft opens with the notice "You were given edit access, but your role cannot
change configs, so this draft opens view only."

### When access changes

When the owner changes someone's access to **Can view**, removes them, or
deletes the draft while they have it open, their next save fails. The save
state says "You cannot save changes to this draft", and a panel opens under
the header: **Your access to this draft changed**. It says why, for example
"You can no longer edit this draft. e2e-admin changed your access to view
only." or "This draft is no longer shared with you, or it was deleted."

To keep changes that were not saved:

- **Save my history as a new draft**: saves the work as a new draft of their
  own.
- **Export**: saves a copy as a file (see
  [Exporting](import-export.md#exporting)).

## Published diagrams

The **Published Diagrams** tab lists each topology that has a Builder v2
diagram, by topology name:

- A topology published from Builder v2, or with `phenix builder publish`
  (see [From the command line](import-export.md#from-the-command-line)),
  with when it was published, for example riverside-water with "Published
  Sep 29, 2026, 10:30 AM".
- A topology whose diagram is read from a file on the phenix server, with
  the tag **File** and where the file is (see
  [Diagrams read from a file](#diagrams-read-from-a-file)).

![The Published Diagrams tab with two cards: riverside-water, Published Sep 29, 2026, 10:30 AM, with Open and Delete; and pump-station with the tag File, Read from /phenix/topologies/pump-station/pump-station.builder.json, and Open only.](../images/builder-v2/published-diagrams.png)

To see one:

1. Select the **Published Diagrams** tab.
2. Select **Open** on the riverside-water card. The diagram opens read only,
   with the notice "You are viewing the published diagram Riverside Water.
   Edit it as a draft to make changes."

![The published diagram Riverside Water open read only, with the notice You are viewing the published diagram Riverside Water and an Edit as a draft button above the toolbar.](../images/builder-v2/published-view.png)

To change it, select **Edit as a draft**. Builder v2 makes a draft from the
published diagram and opens it. The next time you do this, it reopens that
draft instead of making another. A draft made this way can publish to the topology again (see
[Publishing again](publishing.md#publishing-again)).

A role that cannot create drafts gets no **Edit as a draft**. Its notice says
"Your role cannot create drafts, so it cannot be edited. Use Export to keep a
copy."

A published diagram keeps who made it and who edited it last, as they were
in the draft when it was published. The Inspector shows them under
**Details** (see [With nothing selected](editor.md#with-nothing-selected)).

### Diagrams read from a file

A topology can name a Builder file on the phenix server as its diagram,
without anyone publishing it (an administrator sets this up, see
[Builder documents in files](administration.md#builder-documents-in-files)).
Its card on the **Published Diagrams** tab differs from a published
diagram's card:

- It has the tag **File**, and says where the file is, for example "Read
  from /phenix/topologies/pump-station/pump-station.builder.json".
- It has no **Delete**. Delete the topology on the **Configs** page instead.

**Open** reads the file and shows its diagram read only, with the notice
"You are viewing the diagram of topology pump-station, read from
/phenix/topologies/pump-station/pump-station.builder.json on the phenix
server. Edit it as a draft to make changes."

When the stored topology is not what the diagram in the file publishes (the
file or the topology changed), the notice adds: "It differs from topology
pump-station as stored. A draft made from it can be published as a new
topology, not as an update of pump-station."

**Edit as a draft** makes a draft from the diagram in the file. The next
time, it reopens that draft while the file still holds the same diagram.
After the file changes, it makes a new draft from the new diagram. Such a
draft can update the topology (see
[Publishing to a topology that names a Builder file](publishing.md#publishing-to-a-topology-that-names-a-builder-file)).

When phenix cannot use the file, the diagram does not open, and the page
says why, for example "Could not open the diagram of topology pump-station.
Builder file /phenix/topologies/pump-station/pump-station.builder.json does
not exist on this phenix server." See
[When the file cannot be used](administration.md#when-the-file-cannot-be-used).
When the file changes between **Open** and **Edit as a draft**, no draft is
made: "Could not create the draft. The Builder file of topology pump-station
changed since it was opened. Open its diagram again."

### Deleting a published topology

**Delete** on a **Published Diagrams** card deletes the Topology config from
phenix. It needs the `configs` `delete` permission on the topology. A card
with the tag **File** has no **Delete** (see
[Diagrams read from a file](#diagrams-read-from-a-file)).

1. Select the **Published Diagrams** tab.
2. Select **Delete** on the riverside-water card.
3. Builder v2 asks "Delete topology riverside-water?" and says "The
   topology is deleted from phēnix. Drafts and experiments made from it are
   not changed."
4. Select **Delete topology**. The card goes from the tab.

A draft that published the topology, was imported from it, or was opened from
its published diagram creates the topology again the next time it publishes
to that name.

## Deleting a draft

1. On the **My Drafts** tab, select **Delete** on the draft's card, for
   example Metro Campus.
2. Builder v2 asks "Delete draft Metro Campus, updated Sep 29, 2026, 12:52
   PM?" and says "The draft and its whole history are removed from the
   server. This cannot be undone."
3. Select **Delete draft**.

When the draft is shared, the message also names who loses access, for
example "alice, bob and carol will lose access too." Deleting a draft does not
change the configs it published.

**Delete** is on your own drafts, and needs the `configs` `delete`
permission. Another user's draft has **Delete** only when it is damaged (see
[Damaged drafts](#damaged-drafts)). An administrator deletes other users'
drafts with the REST API (see
[Other users' drafts](administration.md#other-users-drafts)).

## Damaged drafts

A draft that this phenix server can no longer read (because a newer version
of phenix saved it, for example) stays on the list. Its card says "This
draft cannot be read, so it cannot be opened. A newer version of phenix may
have saved it." It has no **Open**, only **Delete**. Open it with the phenix
version that saved it, or delete it.

## Session expiry

### Signing in again

When phenix has user sign-in, your session ends after a while (see
[Troubleshooting](administration.md#troubleshooting)). When that happens
while Builder v2 is open, the **Sign in again** dialog opens: "Your session
has ended. Sign in again to keep working; changes not saved yet stay in this
browser until then."

![The Sign in again dialog: Your session has ended, the read-only Username alice, an empty Password field, and the Cancel and Sign in buttons.](../images/builder-v2/signin-again.png){ width="432" }

1. Enter your password in **Password**. **Username** shows your user name and
   cannot be changed.
2. Select **Sign in**. Builder v2 sends the changes that waited.

A wrong password shows "The password is incorrect." To sign in as someone
else, select **Cancel**, then log out.

**Cancel** keeps your changes in this browser. A notice then says "Your
session has ended. Changes not saved yet stay in this browser until you sign
in again." Select **Sign in again** in the notice to open the dialog again.

### Logging out

When you select **Logout** in the phenix navigation bar, Builder v2 first
sends the changes that are still waiting. If some cannot be sent (while
offline, for example), it warns you:

> **Log out with unsaved changes?**
>
> 1 change to Builder v2 drafts has not reached the server. Logging out
> deletes it from this browser. Use Export to keep a copy.

- **Export**: saves each draft with unsent changes as a file.
- **Stay signed in**: closes the warning. Builder v2 keeps sending.
- **Log out anyway**: logs out and deletes the unsent changes from this
  browser.

After a long time without activity, phenix logs you out by itself. The
warning then says "You will be logged out" and counts down, and its button
is **Log out now**.

Logging out, or signing in as another user in the same browser, deletes
Builder v2's unsent changes and recent commands from the browser. Your
preferences stay: the theme, column widths, minimap size, keyboard shortcuts
and settings (see [Storage](administration.md#storage)).
