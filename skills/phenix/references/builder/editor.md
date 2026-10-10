# Builder editor

Part of the [Builder references](../builder.md). The editor page: opening
diagrams, the Inspector, notes, layouts, signing in again, the header and
toolbar, keys and the palette, the side columns and the minimap, and the
lists of checks with Go to.

## In the browser

The
Inspector suggests drive images, and the diagram checks flag a missing one,
from `GET /disks`; without the `disks` `list` permission it does neither.

### Opening diagrams, the Inspector, notes and layouts

Builder works over plain HTTP as well as HTTPS. A published diagram
opens read only; Edit as a draft creates a draft from it (or reopens the draft
made from it before), which needs `configs` `create`, so a role with only
`list`/`get` can view drafts and published diagrams but cannot create, import,
upload, or publish. On the Configs page the Builder tag of a topology
(`builder` for `builder-doc`, `builder legacy` for `builder-xml`) is a
`<router-link>`, and the viewer has a button left of Edit Config ("Open in
Builder" for `builder-doc`, "Import into Builder" for any other topology,
the latter only with `configs` `create`); both, and the edit button of a
`builder-doc` topology, go to `builderLink(config)` =
`/builder?topology=<name>` (`builderAction`, `builderLink`,
`builderTagLabel` in `builder/configs.js`). `openLinked` in `Builder.vue`
then opens, for a topology with a current published document,
`draftForPublished(drafts, published, token)` in this order: the user's
draft that published it, a draft shared with the user for edit that
published it, the user's draft made from it (`builder-doc/<id>` token),
else a new draft (`store.openPublishedDocument`); without `configs`
`create` and no such draft, the published diagram read only. Any other
listed topology (plain, legacy, or `builder-doc` naming no document) opens
the Import dialog with `initial: {kind: 'topology', name}` and nothing is
made until Import; errors: "Topology <name> does not exist, or you may not
read it." and "Topology <name> has no Builder diagram, and your role cannot
create drafts to import it. Select its name in Configs to view it." The
address then names the draft as `?draft=<owner>/<id>`, so a reload reopens
that draft. Edit on a `builder-xml` topology opens the text editor with
`builder-xml: <SNIPPED>`; the diagram is written back unchanged while that
line stays, and deleting the line removes it. The Inspector also edits a node's labels,
annotations, and advanced (minimega `vm config`) settings, and its notes
(`general.notes`), each note in a text area of its own (`isMultilineList` in
`inspector/control.js`). A switch's form edits the switch's own notes
(`switch.notes`) the same way, applied with its network's fields in one undo
step. Device and switch notes (`nodeNotes` in `model.js`; blank ones and
non-text entries show nothing) show in a card below the node, inside Vue
Flow's wrapper but outside the node's box (`nodes/NodeNotes.vue`: five notes
at most, each cut after three lines, then "+N more"), in the info tooltip and
the node's description (`nodeInfo.js`; the card is `aria-hidden`, so the
description says the five notes it shows, each whole up to `noteCharacters`,
then "and N more notes"), and in PNG and SVG downloads (`documentBounds` in
`exporters.js`). Copy, paste and duplicate keep them (`clipboard.js`).
Duplicate (`store.duplicate`) pastes `copySelection` of the selection as it
is, through `pasteClipboard`, and never reads or writes `store.clipboard`;
with no node selected it does nothing and says why (`DUPLICATE_NEEDS_NODES`,
also the reason `edit.duplicate`'s `when` gives), since a connection is
copied only with both of its nodes. The
setting `showNodeNotes` ("Show node notes", palette `view.nodeNotes`, which
announces "Node notes shown." or "Node notes hidden.") hides the cards. Every
layout places a node by `nodeFootprint` (`nodeNotes.js`): its box plus the
card's height, estimated from the `.builder-node-notes` measures in
`builder.css`, wrapping each note at spaces as the card does. An interface's kind
picker offers Static or OSPF, DHCP or manual, and Serial; the Protocol and Type
fields under it hold the rest. With nothing
selected, its Diagram section shows two more parts below Name and
Description. Annotations lists the annotations of the config the diagram was
imported from, sorted by key and without `builder-` ones, under "From <Kind>
<name>, imported <time>"; a diagram drawn in the editor has no such part. A
value too long for its box scrolls in it, and Tab reaches the box only while
it scrolls. Above Annotations, a read-only Details block shows the document's
provenance (see [Document provenance](document.md#document-provenance)): "Created
<time> by <createdBy>", "Last edited <time> by <updatedBy>", and "Source file
<name>" when the draft record has `sourceFile`; a row without a value is
left out, and the block when no row is left. Scenarios lists each scenario
the document names ("Scenario <name>", testids `inspector-scenario-N`) with
each of its apps and the hosts it runs on, or "No scenarios.". The apps are
read with `GET /configs/Scenario/<name>` (`store.fetchScenario`, once per
name until `saveScenarioConfig` drops the reading), which needs `configs`
`get`; otherwise the Inspector says it cannot read them. Edit scenarios
(Add scenario when there is none) opens the same Scenario dialog as the
toolbar's Scenarios button; a read-only draft shows neither. Leaving a draft,
publishing, or downloading first saves Inspector changes that were not applied,
as a draft snapshot with the summary `Saved unapplied changes to <node>`.
Logging out removes Builder's local drafts (IndexedDB `phenix-builder`),
recent commands, the last Auto-group name pattern and the remembered
Preflight checks (`phenix.builder.preflight`) from the browser, as does
signing in as a different user
(`phenix.builder.user` names whose data the browser holds), and keeps its
preferences (`phenix.builder.theme`, `phenix.builder.panes`,
`phenix.builder.minimap`, `phenix.builder.shortcuts` and
`phenix.builder.settings` in localStorage).
Logging out first sends changes still queued in the browser; if some remain,
a warning offers Download (one file per draft), Stay signed in (not once the
token has expired) and Log out anyway. The idle timeout and an expired token
show it for one minute (an expired token's only while the tab is visible),
then log out; for an automatic logout of an expired token on the Builder page
it also offers Sign in again. A draft keeps in its document's `layout` the
layout that last laid it out: the one chosen in the toolbar's layout menu, or
the one Auto layout or Auto-group ran. The layout menu names it, or says
Default, with no layout checked, for a draft without one (imported, uploaded,
blank or placed by hand). On such a draft, Auto layout and Auto-group run the
Settings layout ("Default layout") and the draft then keeps
it. A run that moves nothing keeps no layout. Undo and Restore previous layout
bring Default back. A document may also hold each connection's `route` as a
layout drew it; publishing and Topology YAML ignore both. ELK layered lays each
network out as a cluster and places the clusters in layers along the
connections between them, cutting a tall layer into slices that wrap into
columns, so connections run left to right, except one ELK reverses to break a
cycle between networks.

### Signing in again

When the server refuses the session (a `401` on a save, a listing or a
publish), or the token expires while Builder is open, a Sign in again
dialog asks for the same user's password; to sign in as someone else, cancel
and log out. The login is sent without the ended session's token, since the
JWT middleware refuses an expired token before the login handler reads the
password. Signing in stores the new token where the last sign-in did
(sessionStorage, and localStorage with Remember me), then sends at once the
changes the server refused, from the open draft and from drafts saving in the
background; it clears nothing. Cancel keeps the changes queued in the browser
and shows a Sign in again notice; Retry saving and Save then open the dialog.
While the dialog is open, navigation within the Builder goes ahead and leaving
it is cancelled. With `VITE_AUTH=proxy`, or a session without a JWT, the save
state still says to use Download and sign in again.

### Header, toolbar and focus mode

Both views share the header buttons at the top right: Commands (⌘K or
Ctrl+K), the theme button (it cycles System, Light and Dark, the same
preference as Settings > Theme), Settings, Help and Focus mode. The drafts page
puts Blank diagram, Import and Upload before them; the editor puts Warnings
and Reset view before them and Shortcuts after the theme button. Below a 97rem
header (about a 1585px window) the theme button, Shortcuts, Settings and Help
show only their icons and Commands drops its key caps; below 77rem (about
1265px) Commands and Reset view show only their icons too. The editor
header takes each step sooner when the labels would move its counts off
center, so in a 1920px window only Reset view and Commands usually keep
theirs. Warnings, Blank diagram, Import and Upload keep their labels. The
editor header shows the
diagram name as text, cut off with an ellipsis when long (whole in its
tooltip), or a muted Untitled diagram. An Edit diagram name pencil after it
(not shown to view-only users) opens a field in its place with the name
selected. Enter or leaving the field renames the diagram, and Escape keeps the
name. After Enter or Escape, focus returns to the pencil. The diagram's counts
are in an outlined box, centered on the header when the name and the actions
leave room; otherwise they move toward the narrower side without covering it.
The name's box stays at least 8rem wide and "Shared by" on one line: the
header wraps before either gives way. Below a 64rem header, where the header
wraps, the counts are centered in the space left on their row. The editor's
toolbar has Add connection and Move to group right before Minimap, Draft
History right after it, then the save state, which is text the toolbar's
arrow keys pass by. Add connection and Move to group open the dialogs "Add a
connection" (`dialogs/ConnectDialog.vue`: Device, Interface, Switch) and
"Move to a group" (`dialogs/RegroupDialog.vue`: Node, Group), whose rules
are in `dialogs/structureForms.js`; the palette's `dialog.connect` and
`dialog.regroup` open them too. They start from the selection, and the
Outline has no such forms. Options that would read alike, such as
unlabelled drawings ("Line (line)") or untitled groups, add the node's
position and, if that is shared too, "n of m" (`optionNames`). F2 in the
Outline renames a node in the field the canvas and the Inspector read
(`renamePatch` in `model.js`): a device's hostname, a switch's network
name, a group's title, a shape's, icon's or line's payload `label`, and a
note's own `label`. In the Publish dialog the hint of a config that
will be updated is a warning (`.builder-hint--warning`, a warning icon and
`--bx-warning-bg`), and the naming rule shows under a name field only while
the name breaks it, after the reason (`configNameReason`, `configNameHint`
in `publish.js`). Focus
mode (⇧⌘F or Ctrl+Shift+F) works on both views and stays on between them,
until the user turns it off or leaves Builder.

### Keys, palette commands and settings

Default keys added in this release: Settings… ⌥⇧S / Alt+Shift+S (editor and
drafts page), Auto layout ⌥⇧L / Alt+Shift+L and Auto-group by network
⌥⇧G / Alt+Shift+G (editor), none of them in text fields, and `N` (Add
device, canvas scope: the canvas or a node). `N` runs `add.device` with
its `keyChoice` (the plain Device; `runCommand` uses a command's
`keyChoice` for a key press, so the palette still asks for a template),
adds it with `addInView`, which selects it, and focuses it
(`view.showNode`). A letter alone is refused as a shortcut
(`keyRefusal`) except for a command whose keys work only on the canvas
(`takesLetters`, `keyRefusal(spec, platform, {letters})`); it is a
single-character key, so the single-key switch turns it off. The Shortcuts
button shows its key (`?`) and Settings' tooltip its own; the list of the
canvas keys is in the user docs' editor page, not under the canvas. The
palette has a command per download format (Download Builder JSON, …,
Download PNG), all found by `export`; `send` finds Share; `legacy` finds
Upload and Import. Auto-group has a third rule, By name pattern… (dialog
"Auto-group by name pattern", a JavaScript regular expression matched
ignoring case, run in a same-origin worker, `grouping.js` and
`groupingWorker.js`: at most 200 characters, the first 255 characters of a
name, 2000 ms; the last pattern is kept in `phenix.builder.groupPattern`
and removed at logout). Settings: "Default layout", and "Zoom when a
diagram opens" with 100%, Fit, or Custom (20 to 200 percent, step 5). Each
header count is a button that selects every item of its kind.

### Side columns, minimap and Fit

Each side column (Add nodes and Outline, the Inspector) has a Hide toggle under
its Widen toggle, which folds the column into a narrow strip holding a Show
toggle; the palette's `view.pane.start` and `view.pane.end` do the same. The
hidden columns are kept with the widths in `phenix.builder.panes`
(`{"hidden": ["end"]}`). A click, Enter or Space that selects a node or
connection shows a hidden Inspector, and Focus outline, Focus Inspector and
Rename show their column first. The stacked narrow layout shows every column
and has no toggles. A handle at the minimap's top left corner resizes it (a
separator named Resize minimap: drag it, or Up and Left for larger, Down and
Right for smaller, Home and End for the smallest and largest, Enter or a
double-click for the default); the palette's Minimap size commands do the
same. The minimap keeps its 4:3 shape, from 120px wide up to half the canvas
(at most 600px, never below the default 200px unless the canvas is too small
to hold it). Its width is kept in `phenix.builder.minimap` (`{"width": 280}`).
Reset view shows both columns again and restores the minimap's default size.
Fit, the canvas's least zoom and bringing nodes into view go by the
diagram's footprint (`footprintBounds` in `nodeNotes.js`): node boxes and,
while Show node notes is on, the notes below devices and switches; Fit sets
the view from it with `getTransformForBounds`, since Vue Flow's own fit
measures only the boxes. Groups hold their members' notes the same way
(`groupNodes`, `fitGroups`, `groupMinimumSize`, `resizedBox`, `setParent`
in `model.js` and `applyGroups` in `grouping.js` take `{showNotes}`).
After a Fit that changes the view (the zoom controls' Fit button, Shift+1 on
the canvas or the palette's `view.fit`), the same button, key and command
restore the zoom and position from before it, and the button is named Restore
previous view. Any other change to the view, including Reset view, drops the
saved view.

## Lists of checks and Go to

The Publish dialog (its checks before publishing, the issues a 409 or 422
refusal lists, and a result's `errorIssues` and `warningIssues`) and the
Diagram checks dialog list issues through `BuilderIssueList.vue`: grouped by
severity, errors first (`bySeverity` in `issues.js`; with the document,
each group in node, then connection, then diagram order), under headings
that count them ("2 errors block publishing" in the Publish dialog's checks
and refusal, else "2 errors"; "1 warning"; `severityHeading`). Each item
says "Error:" or "Warning:", the message with index references named
(`issueText`), the element (`Device web-01`), the server's `code` in a
monospace badge, and Go to (testid `issue-go-to`, named "Go to <element>:
<message>", `goToName`) when the diagram has the element. Testids: list
`publish-checks`, `publish-refusal`, `publish-result-issues`,
`checks-issues`; groups `<list>-error`, `<list>-warning`; items `issue`,
`issue-message`, `issue-element`, `issue-code`.

Every issue goes through `toIssue(entry, defaultSeverity, {publishing})`
into `{code?, severity, message, path?, nodeId?, edgeId?, networkId?,
field?, blocksPublish?}`: a string, a `validateDocument()` issue (`level`;
with `publishing`, a `blocksPublish` warning is an error, as
`publishChecks` does) or a server issue object; `responseIssues(data)` reads
a body's `errors` then `warnings` (strings or objects), then `issues`
(objects, an error unless they state a severity), listing an issue given
twice once (a message alone that says what an issue object says, as
`path: message`, gives way to the object), which `store.publish` keeps as
`store.publishIssues` for a 409 or 422. The Publish dialog's
`useRefusalIssues` (`dialogs/message.js`) clears them as it opens and
closes and once `store.doc` changes other than its stamp (`sameButStamp`).
`issueTarget(doc, issue)` resolves `nodeId`, `edgeId`, `networkId` (the
network's first switch), else the element the `path` starts at (as
`locate()` in `validate.js`; JSON pointers and dotted indexes are read
too), with `issueField`: the issue's `field`, else the path below the
element, as a JSON Forms data path (`nodes[2].device.spec.network.
interfaces[0].vlan` is `spec.network.interfaces.0.vlan`; a switch's
network issue `networks[0].name` is `name`). No element, no Go to.

`store.goToIssue(issue)` selects the target and sets `store.focusRequest =
{kind, id, field, token}`. `Builder.vue` shows a hidden Inspector and
reveals the node (a connection's two ends); without a field it announces
"Selected <name>" and focuses the canvas element as keyboard focus. With a
field, `BuilderInspector.vue` (`goToField`) focuses the control whose data
path is the field (or the first field inside it, opening closed sections,
falling back as the error summary does) and announces "<field label> in
<element>", e.g. "Hostname in device web-01"; with no control, the
Inspector heading takes focus. A dialog closes before the request, so focus
returns to its opener first. The Inspector takes a request with a field
once (`store.takeFocusRequest(token)` marks it `taken`): when the token
changes (a `post` watcher) and as it mounts, so a request made before it
mounted is acted on. In landing mode the Publish dialog's Go to emits
`open-draft` with the issue, and `openPublished` opens the editor and calls
`goToIssue` at once; `Builder.vue`'s watcher runs `post`, once the editor
is drawn. The Inspector's own Checks list gives an issue about one of its
fields a Go to (`inspector-check-go-to`, "Go to <field>: <message>").
