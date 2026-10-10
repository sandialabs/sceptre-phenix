# Builder

The Builder is phenix's web topology editor, built on Vue Flow. This file
holds everything about it that the main phenix skill leaves out.

**Read this file when** a task involves Builder: its drafts, sharing,
publishing, import, upload, download or generation, the conversion of legacy
diagrams, custom icons, node templates and their libraries, its
`/api/v1/builder/*` or `/schemas/builder/v1` routes, the `builder-drafts` and
`builder-templates` RBAC resources or the built-in Builder role, the Builder
document format (`builder/v1`), the `builder-doc` annotation, Builder files
named by `builder-doc.path`, `phenix builder publish`, or any Builder code
(see
[Working on Builder code](#working-on-builder-code)). The legacy
Builder of earlier releases (mxGraph, `builder-xml` topologies) was removed
in sandialabs/sceptre-phenix#442 (the last commit with it is `a0aeaa4e`);
topologies it saved still carry `builder-xml`, and the Builder converts
them (see [Legacy import](#legacy-import)).

Words, as the UI and the user docs use them: **Import** makes a draft from a
phenix config (stored, or a config file; `POST /builder/generate`);
**Upload** opens a file that is already a diagram (a Builder document, or a
legacy diagram through `POST /builder/legacy`), or a published diagram;
**Download** saves the open diagram as a file. The Builder is in every
`phenix ui`; nothing turns it on or off, and RBAC is the only control.

## Where it runs

The Builder, the Vue Flow topology editor, is on in every `phenix ui`, at
`/builder`, with its draft and document APIs under `/api/v1/builder`.
Its one CLI command is `phenix builder publish`, which makes a Topology from
a Builder document file and needs no running server
(see [CLI: phenix builder publish](#cli-phenix-builder-publish)); drafts,
sharing and everything else are in the REST API and the web UI only.
Drafts autosave separately from phenix configs; in the web UI and REST API
only the explicit Publish action creates or updates topology or experiment
configs and adds the topology to the `topology` annotation of the
document's scenarios, and only the Scenario dialog stores a Scenario
config (from an uploaded file, with `POST`/`PUT /configs`). The five built-in device templates (`server`,
`workstation`, `router`, `firewall`, `external`; fixture
`types/builder/testdata/builtin-templates.json`, `BuiltinTemplates()` in Go,
`BUILTIN_TEMPLATES` in `catalog.js`) write no description into the node.
Router is type `Router` running `minirouter` on `minirouter.qc2`; Firewall
is type `Firewall` running `vyos` on `vyos.qc2`. The
Inspector suggests drive images, and the diagram checks flag a missing one,
from `GET /disks`; without the `disks` `list` permission it does neither.

## In the browser

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
`exporters.js`). Copy, paste and duplicate keep them (`clipboard.js`). The
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
provenance (see [Document provenance](#document-provenance)): "Created
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
Logging out removes Builder's local drafts (IndexedDB `phenix-builder`)
and recent commands from the browser, as does signing in as a different user
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
Outline has no such forms. In the Publish dialog the hint of a config that
will be updated is a warning (`.builder-hint--warning`, a warning icon and
`--bx-warning-bg`), and the naming rule shows under a name field only while
the name breaks it, after the reason (`configNameReason`, `configNameHint`
in `publish.js`). Focus
mode (⇧⌘F or Ctrl+Shift+F) works on both views and stays on between them,
until the user turns it off or leaves Builder.

Default keys added in this release: Settings… ⌥⇧S / Alt+Shift+S (editor and
drafts page), Auto layout ⌥⇧L / Alt+Shift+L and Auto-group by network
⌥⇧G / Alt+Shift+G (editor), none of them in text fields. The Shortcuts
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
After a Fit that changes the view (the zoom controls' Fit button, Shift+1 on
the canvas or the palette's `view.fit`), the same button, key and command
restore the zoom and position from before it, and the button is named Restore
previous view. Any other change to the view, including Reset view, drops the
saved view.

## Access, sharing and RBAC

Draft owners can manage their own drafts. An owner can share a draft with
named users as `view` or `edit`. `GET /builder/drafts/{owner}/{draft}/shares`
lists them (owner only). `PUT` of the same path with `If-Match: "shares-N"` and
`{"shares":[{"user":"bob","access":"edit"}]}` replaces the list: at most 25
users, and unknown users, the owner and duplicates get `422` with per-user
`errors`. A share gives view (`get`/`list`) or edit (also save, undo and
publish under the recipient's own permissions). It never gives delete or
sharing. Recipients still need their `configs` permissions. A share is bound
to the recipient's account, so a user deleted and recreated under the same
name loses it. `GET /builder/drafts/{owner}/{draft}/shares/candidates` lists
who the draft can be shared with, as
`{"users":[{"username":"alice","name":"Alice Tester"}]}` sorted by username
(`name` is the first and last name, or `""`). It holds every account that can
receive a share, whatever the caller's `users` permissions: it leaves out the
owner and anyone a `PUT` would refuse, and lists users already shared with. It
has the same access rules as `PUT .../shares`. The Share
dialog offers these users in a drop-down that filters as the user types, shown
as "Name (username)" and without those already listed. If the list cannot be
read, the dialog shows the error with Retry, and a typed username can still be
added (the server checks it on save). Cross-user access for administrators uses the
`builder-drafts` RBAC resource with `{owner}/{draft-id}` resource names. A role
that may inspect and modify every draft needs an explicit policy like:

```yaml
- resources: [builder-drafts]
  resourceNames: ["*/*"]
  verbs: [list, get, update, delete]
```

`create` is deliberately not a `builder-drafts` verb: a draft is always created
for the authenticated user, never on somebody else's behalf. Resource names are
matched with `filepath.Match`, which does not match `/`, so a bare `"*"` never
matches a `{owner}/{draft-id}` name — use `"*/*"` (as `global-admin` does) or an
explicit `alice/*`.

Every Builder request also needs the base `configs` permission of the verb
it performs (`list`, `get`, `create`, `update`, `delete`), so builder access can
never exceed a user's config access. A caller who cannot see another user's
draft (no share, no `builder-drafts` permission) gets `404`, so draft existence
is never disclosed. A caller who can see it but may not perform the operation
(a viewer saving, an editor deleting or sharing) gets `403`. Every mutation
after creation requires an `If-Match` header carrying the quoted ETag the
previous response returned: a missing or malformed tag is `400`, a stale one
`412`. Draft responses also carry the tag in their body as `etag`; prefer it,
since a compressing proxy can rewrite the header (`W/"3"`, `"3-gzip"`). Draft
responses carry `access` (`owner`/`edit`/`view`), `via` (`share`/`role`) for
other users' drafts, and, for the owner, `canShare` and `shares`.
`GET /builder/drafts` lists other users' drafts the caller may see in `shared`,
those shared with the caller with `via: "share"`. Changing shares changes the
draft's ETag; the share list has its own `"shares-N"` tag. A draft that has
ever been shared shows as damaged (its owner can delete it) on a phenix
version without sharing. `GET /builder/drafts` also returns `damaged`: drafts
whose metadata this server can no longer read (written by a newer phenix, say),
with `id`, `owner`, `etag`, `canDelete`, and `title` and `updated` when they
can be read. They cannot be opened, only deleted with that `etag`; a `412` on
`DELETE` also carries the current `ETag`.

Publishing still requires the applicable config, scenario, and experiment
permissions; Builder draft access does not bypass them.

`GET /builder/drafts` rows and draft responses carry `canDelete` (computed
on every read, never stored): another user's draft has it when the role
holds `builder-drafts` `delete` and `configs` `delete`, and the drafts page
then offers Delete on its card. Draft responses (one draft, `POST
/builder/drafts`, the draft in a publish answer; never the listing) carry
`experiment`, and stored rows of `GET /builder/documents` too: the name of
an experiment the publication made that still exists, read from its
`builder-experiment` annotation on every request, only for a caller with
`experiments` `get` on it (one is named: the draft's last publication's,
then one recording the document, then the first by name).

`builder-templates` `publish` (no resource names; a literal check,
`builderTemplatesPublishAllowed` in `web/builder.go`, so `make generate`
records it) lets a role publish template library items to every user and
take any user's server-wide item back. The icon and template libraries are
the caller's own: no role, also not Global Admin or `builder-drafts`, reads
or changes another user's (404), except taking back a server-wide item.

The built-in role `Builder` (`api/config/default/builder.yml`,
`metadata.name: builder`) holds every Builder permission: `configs` all five
verbs on `Topology/*`, `Scenario/*`, `Experiment/*` only (never `*/*`, which
would expose User and Role configs: password hashes and role changes),
`builder-drafts` `list`/`get`/`update`/`delete` on
`*`/`*/*`, `builder-templates` `publish`, `schemas` `get`, `topologies` and
`scenarios` `list`/`get`, `experiments` `list`/`get`/`create`/`update`,
`disks` `list`. `rbac.EnsureBuilderTemplatesPublishPermission` (called from
`web.Init` at every start) creates it when no role is named `builder` or has
role name `Builder` (so old stores get it, and a deleted one comes back),
and adds the `builder-templates` `publish` policy to an existing role of
that name and to the users assigned it, changing nothing else. Of the other
built-in roles only Global Admin can publish templates (`*`). The example
roles `docs/content/builder/examples/roles/topology-*.role.yaml` are for
sites that want less.

## Saving: snapshots, ETags and the local queue

A snapshot append (`POST /builder/drafts/{owner}/{draft}/snapshots`) may carry
`opId`, the client's id for the save (1-128 letters, digits, `.`, `-`, `_`,
starting with a letter or digit). The snapshot manifest keeps it, and snapshot
listings return it, so a client whose response was lost can tell the snapshot
was stored. A draft keeps at most 50 snapshots and 50 MiB of them; past either,
the oldest are dropped. A last publication naming a dropped snapshot is kept,
as when that snapshot is deleted: the draft is then dirty, and it (or a draft
that forks it) can still update the topology or experiment it published. The
UI keeps unsaved Builder edits in the browser's `phenix-builder` IndexedDB
database only until the server confirms them. Each browser tab keeps its own
queue of a draft (the tab's id is `phenix.builder.tab` in sessionStorage). When
more than one tab holds unsaved changes to one draft, the user chooses which to
save, and the others are saved as new drafts. Logout leaves the changes of
other open tabs to those tabs. It finds them by their Web Locks or, without Web
Locks, by the tabs that answer within 500 ms over the BroadcastChannel
`phenix-builder:tabs` (localStorage events without one).

`GET .../snapshots` returns `{"cursor": <index>, "snapshots": [...]}`, oldest
first; each snapshot has `id`, `digest`, `size`, `createdAt`, `createdBy`,
`current`, and `summary` and `opId` when set.
`DELETE /builder/drafts/{owner}/{draft}/snapshots/{snapshot}` with `If-Match`
removes a version and its content. Those who may save may delete (the owner,
an edit share, `builder-drafts` `update`). It answers 200 with the draft (as
`POST .../snapshots` does) and its new `ETag`, and 409 `The current version
cannot be deleted.` for the cursor's snapshot (also as `current`), 404 for an
unknown snapshot, and the usual 400, 403, 404 and 412. The cursor stays on the
same snapshot. A last publication naming the deleted snapshot is kept, and the
draft is then dirty. Snapshots never share chunks, so no other version or
published document loses content. The editor's Draft History is a table of
number, name, date and user, newest first and numbered from the oldest (1, the
draft as created), with Restore and Delete in each row (clicking a name also
restores). The current row can be neither restored nor deleted, a view-only
user gets no actions, and undo and redo skip a deleted snapshot. In a narrow
window a row's Restore and Delete tooltips show above the row, or below it
when the row is at the top of the dialog, so they cover none of the row. After
a delete, focus moves to the Delete of the next older snapshot, or of the row
above when the last row goes. After a 412 the editor reads the draft again and
the next try uses that ETag.

A mutation whose durable write succeeded but whose superseded content could not
be removed returns its normal success status, body, and new `ETag`, plus a
`Warning: 199` header naming the operation; the cause is logged, never sent.
Failing such a request would only make the client retry with a stale tag.

## Sources and generation

`GET /builder/sources` groups configs by kind: `topologies` and `experiments`
(what a document can be generated from, reported as `generatable: true`),
`scenarios` (what the Scenario dialog offers; no `digest`) and `images` (node property editing;
empty for the built-in Builder role, which has no `configs` on `Image/*`).
Each config is filtered through the `configs` permission *and* the kind specific
`list` permission that already gates the kind elsewhere (`topologies`,
`experiments`, `scenarios`); `Image` configs have no kind specific vocabulary,
so `configs` is their only gate. Generating from a non-generatable kind is
`422`. VLANs are derived from the document and are not a config kind.
Generating from an Experiment drops, without a warning, the injections its apps
added when it started: those whose `src` is an absolute path under the
experiment's base directory (its `baseDir`, or `<phenix base>/experiments/<name>`).
The topology's own injections are kept.

Generation copies the source config's `metadata.annotations` into the
document's `source.annotations`. It leaves out every `builder-` annotation
(`builder-xml`, `builder-doc`, `builder-experiment`) and keeps the others, such
as an experiment's `topology` and `scenario`. They are shown only: they do not
change `source.digest`, and publishing never writes them. A document holds at
most 100 annotations and 256 KiB of keys and values in all. Keys must not be
blank, must be at most 512 bytes long and must not contain control characters.
Generation keeps the annotations that fit, in key order, and warns about the
rest. `POST /builder/generate` also sets `source.importedAt` (RFC 3339, UTC).

`POST /builder/generate` accepts either `{"source":"Topology/name"}` (or an
Experiment source) or `{"content":"..."}` containing an uploaded JSON/YAML
Topology or Experiment, plus the import options `includes` (`""`/`"keep"`,
or `"combine"`), `copy` (bool) and `name` (the new topology name; only with
`copy` or `combine`, else 400). `combine` (`bdoc.WithCombinedIncludes()`,
then `Document.CombineIncludes` in `types/builder/detach.go`) makes every
readable included node the document's own; includes that could not be read
(missing, forbidden, a file path, past the 100th) stay in
`source.includeTopologies` with the warning `Included topology X was not
combined and stays in includeTopologies: publishing keeps the reference.`
`copy` or `combine` then run `Document.Detach(newName)`: `source.kind`
`manual`, no config named, document name = the new name (default
`<name>-copy` / `<name>-combined`; 1 to 512 bytes, config name pattern; not
the stored source's own name, else 422; the server does not look for a free
name, the dialog proposes one with `uniqueName`). Neither is allowed for an
Experiment (422 `only a topology can be combined or copied on import`). The
draft of a copy or combine gets source token `''` (stored source) and so
publishes a new topology and cannot update the source. `source.unresolvedIncludes`
(keep mode) lists included topologies whose nodes are not in the document;
informational, Publish ignores it. A config file needs `metadata.name`
(422 otherwise). `GET /builder/sources` topology rows gain `includeCount`
(left out when 0; only for a topology the caller may `get`), which the
Import dialog uses to offer "Included topologies" (`dialogs/importOptions.js`,
`uploadedConfigInfo` for a file). A stored topology that has `builder-xml`
and no `builder-doc` is converted from its legacy diagram by
`(*builderAPI).generate` (see [Legacy import](#legacy-import)). The editor
command "Combine included nodes into a new draft" (and the Inspector button
"Combine into a new draft" under an included device's lock note) does the
same as combine on the open draft, client side (`combineIncluded` in
`model.js` and `store.js`), into a new draft with a name no topology and no
draft of the user's has. Uploaded sources are reported as `stored: false` and
receive uploaded provenance, so they can create publication targets but cannot
authorize a Topology or Experiment update. Uploaded `content` needs `configs`
`create` (a stored `source` needs only read permissions): uploads are parsed
like `POST /configs`, including `${NAME}` / `${NAME:default}` substitution from
the server's environment, so anyone allowed to create configs can read the
server's environment variables (sandialabs/sceptre-phenix#436 describes this).

Generation resolves a Topology's `includeTopologies` recursively, the way
phenix merges them, and adds the included nodes as devices marked
`device.includedFrom: <defining topology>`; for an Experiment, whose topology
phenix already merged, the included nodes are recognized and marked instead
(a hostname the experiment's own topology defines is never marked). When an
include cannot be read now, a node that neither the experiment's own topology
nor a readable include defines is marked as coming from it (from the first,
when several cannot be read).
Includes are read from the config store only (never file paths), under the
caller's `configs` `get` and `topologies` `list` permissions. (The one server
file Builder reads is a Builder file named by `builder-doc.path`; see
[Builder files](#builder-files).) A missing,
forbidden, cyclic, or repeated include, or a hostname that collides with
another topology's, is reported as a warning. Included devices are read only in
the editor (they can be moved, not changed, deleted, or reconnected), and
publishing omits them and writes `includeTopologies` instead, so a round trip
does not duplicate them. The exception is an Experiment whose own topology
and one of whose includes both cannot be read: that include's nodes cannot be
told from the topology's own, so they are imported as its own (with a warning)
and a topology publish copies them in, where phenix finds them twice. Publish
answers 409 when an included topology now defines a hostname the published
topology also defines, and an experiment update, which merges the includes
itself, answers 403 or 422 for an include the caller may not read or that is
not a stored topology.

## Scenarios and publishing

A document names the Scenario configs it is used with in root `scenarios`
(`bdoc.Document.Scenarios`): at most 20 (`MaxScenarios`, `MAX_SCENARIOS`),
each a config name (`^[A-Za-z0-9_@.-]+$`) of at most 256 bytes
(`MaxScenarioNameBytes`, the bound on a publication's targets), none twice
ignoring case, left out when empty. It holds no scenario content: there is
no `scenario` object (strict decoding refuses that key, with no shim), no
uploaded kind and no digest. Go (`validateScenarios`) and JS
(`validate.js`, `scenarioNameProblem`; `decode.js` refuses a non-list or a
non-text entry, and validation an empty one) check it alike through the
shared corpus.

Generating from an Experiment (stored or a file) lists its `scenario`
annotation when that names a Scenario config the caller may list on this
server (`configs` `list` and `scenarios` `list`;
`bdoc.WithScenarioResolver`, `storedScenarioResolver` in
`web/builder_sources.go`); otherwise it lists none and warns `the
experiment's scenario "<name>" is not a stored Scenario config and was not
attached` (`the experiment's scenario is not ...` when it names none but
holds content). The experiment's embedded copy is never attached. For a
file it also warns `scenario "<name>" is this server's Scenario config of
that name, not the copy the experiment file holds`. A store error answers
500.

The Scenario dialog (`dialogs/ScenarioDialog.vue`, title "Scenarios",
testids `scenario-…`) edits the list: Remove, Add a stored scenario (the
sources not listed), and Upload a scenario file (`phenix.sandia.gov/v2`
only; name from `metadata.name`, else the file name through `configName`,
editable). Store and add creates the config with `api.createConfig` (`POST
/configs`, JSON `{apiVersion, kind: Scenario, metadata: {name,
annotations?}, spec}`), or, for a name the sources list, after the
confirmation "Replace scenario <name>?" (it says the spec is replaced and
the annotations kept), replaces it with `api.updateConfig` (`PUT
/configs/Scenario/<name>`). A PUT replaces the annotations too, so
`store.saveScenarioConfig` first reads the stored config
(`api.getConfig`, `GET /configs/Scenario/<name>`) and sends
`replacedScenarioConfig(stored, file)` (`builder/publish.js`): the stored
annotations with the file's over them, and the `topology` annotations
merged by `mergeTopologyAnnotation` (the stored value byte for byte, then
each file name it lacks, after a comma), so no topology is taken off a
scenario. The server's refusal, of the read or the write, shows in the
dialog's alert (`serverReason`). `store.saveScenarioConfig` then reads the
sources again. A stored name the list already has in another letter case
replaces that entry with the stored config's spelling (the status says
"the list names it <name> in place of <old>"); one listed exactly is left
("The list already names it."). Save writes the list with
`store.setScenarios` (one undo step "Updated scenarios"); Cancel keeps a
stored scenario on the server but not in the list.

Publish (`preflightScenarios`, `publishScenarioStage` in
`web/builder_publish.go`), in either mode, runs a `scenario` stage after the
topology and before the experiment whenever the draft's document lists
scenarios: each must exist and be readable (`configs` `get`, `scenarios`
`list`), else 422 `scenario <name> does not exist` (a hidden one alike);
one whose `topology` annotation does not name the topology exactly
(comma-separated, trimmed; `hasTopologyAnnotation`) gets it added
(`addTopologyAnnotation`: appended after a comma, the rest of the value
kept byte for byte, or the whole value when it was empty), which needs `configs` `update` on it (else 403
`adding topology <t> to scenario <s> not allowed`); others are not written.
The one stage reports `updated` or `skipped`, a message such as `added
topology t to scenarios a, b; scenario c already names it`, and `config`
only when one scenario is listed. A failed write leaves the earlier ones
written (a warning names them); publishing again resumes. The request's
`scenario` is `{name}` only, allowed only in `topology-experiment` mode
(400 otherwise, and for a name outside the config rule or any other key),
and must be one of the document's `scenarios` (422 `scenario <name> is not
one of the scenarios this draft lists`); the experiment is created with it
(`CreateWithScenario`, after the stage annotated it) or updated with it
(`MakeCustomScenarioFromConfig`, `MergeScenariosForTopology`, the
`scenario` annotation); none leaves the experiment without one. The
publication records it as `scenarioTarget`. The Publish dialog's
Experiment scenario select (`publish-scenario`) lists the document's
scenarios and No scenario, the first by default, and its hint
(`scenarioStageHint`) says the topology is added to each listed scenario.
`phenix builder publish` changes no scenario and notes `The document's
scenario is not changed: only the topology is published.` (`The
document's N scenarios are not changed: …`).

Topology and Experiment updates require a draft tied
to that exact stored source: one imported from it, or one that published it (or
was opened from the published diagram that did), with nothing else having
changed it since. Otherwise the update gets 409, for example `topology <name>
changed after this draft published it` or `experiment <name> changed after this
draft published it`. `POST /builder/drafts` accepts
`forkOf: "<owner>/<draft id>"`, which saving the editor's history as a new
draft sends: the new draft takes that draft's source token and records its last
publication as `forked`, so it can update what that draft published or was
opened from (not what that draft publishes later). The caller must be able to
read that draft (owner, a share, or `builder-drafts` `get`), otherwise 404. A
`sourceToken` of `builder-doc/<document id>` needs `configs` `get` for the config
that document was published to, otherwise 404. A `sourceToken` of
`builder-file/<topology>/<digest>` names the Builder file a topology
references (see [Builder files](#builder-files)). A published
topology names its document in its `builder-doc` annotation (see
[The builder-doc reference](#the-builder-doc-reference)); a published
experiment records the draft and document that published it, and its digest
after the configure stage, in its `builder-experiment` annotation, so any later
change to its spec counts. An Experiment update then runs the apps' configure
stage, as `PUT /configs` does. A failed configure stage leaves the experiment
unchanged and is reported as a `partial` result, and an experiment found
running once its lock is held is too. An Experiment create is refused with 422
before anything is written if its name is `all` (in any case), or longer than
15 characters in auto bridge mode. A name outside the config naming rule is
refused with 400.

Publish answers 422 when an interface of a device that is not external has no
VLAN, and the error `message` names the devices and interfaces (the first three,
then how many more; by position, such as `#2`, when unnamed or when two share a
name): phenix would store such a topology, but minimega refuses the interface
when the experiment starts. Connect the interface or give it a VLAN. A VLAN
that names no network of the document still publishes as it is, since phenix
allocates VLANs by name and matches them exactly (`exp` is not network `EXP`).
Drafts keep such interfaces; the editor flags them as warnings.

Publish also answers 422 when interfaces use the same IP or MAC address, and
the error `message` names each address and the interfaces that use it (the
first three addresses, then how many more): phenix would store such a
topology, but the addresses clash once the experiment runs. IP addresses are
compared parsed, without a prefix length typed after them, and MAC addresses
in any case and with any separators. The IP addresses of interfaces whose
`proto` is `dhcp` or `manual`, blank values and external devices' MAC
addresses are not compared. Included devices are, but an address only they
use is left to their topology. When interfaces also have no VLAN, the 422
names only those. Drafts keep shared addresses; the editor flags each
interface that uses one as a warning, and the Publish dialog lists them as
errors.

Publish also answers 422 for a hostname of a device that is not external
which phenix refuses, and the error `message` gives phenix's reason for each,
which names the hostname (the first three, then how many more): one character
long, which phenix's schema refuses, or `all`, all digits, or `phenix` on a
Windows node, which phenix stores but refuses when it creates an experiment.
When interfaces also have no VLAN or share addresses, the 422 names only
those. Drafts keep such hostnames, as a topology an older phenix stored may
have them; the editor flags each as a warning, and the Publish dialog lists
them as errors. It shows phenix's warnings about other casings of `all`, and
about `phenix` on a node that is not Windows, as plain warnings, and Publish
returns them in `warnings`. Included devices are left to their topology, but
an experiment publish whose included topology has a hostname phenix refuses
in an experiment (any of the above, one character long included) answers 422
before anything is written.

The Download dialog's Topology YAML saves `<diagram name>.topology.yaml`, the
Topology config Publish would write, from `POST /builder/export/topology`
(`configs` `get`; nothing is written). The request carries the document,
edits not yet saved included, and the topology `name` the Publish dialog
proposes. The config has no annotations and names included topologies in
`includeTopologies` rather than merging them. What only Publish refuses,
interfaces with a blank VLAN, shared addresses and the hostnames phenix
refuses when it creates an experiment, is not refused but named in
`publishBlockers`, which the dialog shows after the download: one entry per
check, interfaces without a VLAN first, then shared addresses, then
hostnames, each as Publish's 422 `message` words it. A document with several
gets each, where Publish's 422 names only the first. A document phenix's
schema refuses is refused with 422, as Publish refuses it. Publish names
interfaces without a VLAN, then shared addresses, then hostnames, before any
other reason; this route names interfaces without a VLAN only when a `vlan`
is missing or null, and hostnames only when one is a single character, which
the schema refuses too, and otherwise gives the schema's reason.

The Download dialog's Gephi (GEXF) saves `<diagram name>.gexf`, a GEXF 1.3
graph for Gephi that Builder cannot open, made in the browser. Devices
and networks are its nodes, each connection an edge from a device to its
network, and their settings are columns; notes, groups and drawings are not
nodes. It lists each device's scenario apps (`apps`, `disabled_apps`) of
every listed scenario, reading each as the Inspector does; when it cannot
read one, it leaves the apps out and
the dialog says why. The file names gexf.xsd in `xsi:schemaLocation`, as
Gephi writes it. The official RelaxNG grammar, gexf.rng, rejects that
attribute (gexf.net says to remove it before using xmllint), so to check a
file as saved, use a grammar that includes gexf.rng unchanged and allows the
attribute on `<gexf>`, the only element that uses `gexf-content`:

```xml
<grammar xmlns="http://relaxng.org/ns/structure/1.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes">
  <include href="gexf.rng"/>
  <define name="gexf-content" combine="interleave">
    <optional>
      <attribute name="xsi:schemaLocation">
        <list><oneOrMore><data type="anyURI"/><data type="anyURI"/></oneOrMore></list>
      </attribute>
    </optional>
  </define>
</grammar>
```

After a publication, older published documents of the same topology are
removed once they are more than an hour old; newer ones go at a later publish
or at the startup cleanup.

Deleting a Topology config, however it is deleted (`DELETE /configs`, the
Configs page, `phenix config delete`, `all` included, or the Builder route
below), removes its published documents too: a Topology config hook, which
`api/builder` registers in every phenix
process, removes those stored before the config was last written and those
more than an hour old. A younger document stored since then may belong to a
publish in flight, which stores its document before it writes the config, so
it is kept; a later publish of the topology or the startup cleanup removes it
once it is more than an hour old. A failure is logged and never fails the
delete.

The config's `metadata.updated` is kept to the second, so the documents stored
before that write are: those stored before that second, the document the
config names in `builder-doc` (unless it was stored again in a later second:
the same content is being published again), and those stored no later than
that document. A document stored in that second after the named one is kept.
The same content published again within that second cannot be told from the
config's own publish and is removed (publishing once more stores it again).
`DELETE /configs` of a topology, and a `PUT /configs` that renames one, hold
the publish lock, so no publish of that phenix process is in flight while the
hook runs; this only concerns another process sharing the store.

Renaming a topology (an update that changes `metadata.name`: `PUT /configs`,
the Configs page, `phenix config edit`) stores it under the new name and
deletes it under the old one. The config hooks of its kind then run a `rename`
stage with the config as it was, where the Topology hook removes the old
name's documents as for a delete. The renamed topology's `builder-doc` loses
its `id`, and its `digest` too unless it has a `path` (see
[The builder-doc reference](#the-builder-doc-reference)), which a copy
stored under a new name gets too. Without a `path` it is a topology like any
other until it is published again. The hooks never read or remove a Builder
file.

`GET /builder/documents` lists only current documents: it lists each kind
of config once per request and matches the documents against their references
(`DocumentReference.Names`), rather than reading one config per document.
Stored rows carry `source: "store"`. A topology the caller may list whose
reference has a `path` and no current stored document gets a file row,
exactly `{source: "file", target, kind: "Topology", config, path}`: no `id`,
`digest`, `size`, `createdAt` or `createdBy`. The listing reads no file, so a
file row is listed even when the file is missing or invalid.

`DELETE /builder/documents/{document}` deletes a published topology: the
Topology config the document is current for, through the same config call and
broadcast as `DELETE /configs`, then every published document of that
topology, however recent: holding the publish lock, no publish of it is in
flight in this process. The route removes the documents itself, to report a
failure: `LeaveTopologyDocuments` has the config hook leave them to it. It
needs `configs` `delete` for the topology. A document the caller may not get,
or one that is no longer current (the topology was published again, points at
another document, or was deleted), gets 404, as `GET` answers. A published
experiment gets 422 `Only published topologies can be deleted here. Delete
experiments from the Experiments page.` If the documents cannot be removed
once the config is gone, the answer is still 204, with a `Warning: 199`
header; such documents are never listed again, and the startup cleanup removes
them once they are more than an hour old. Drafts and experiments made from the
topology are not changed. On
the drafts page, each Published Diagrams card of a published topology has
Delete, for a role with `configs` `delete`; a File card has none. It asks
first ("Delete topology <name>?"), is
aria-disabled and says Deleting… while it runs, then removes the card,
announces `Deleted topology <name>.` and moves focus as for a deleted draft
card. A refused delete keeps the card and shows the error; a 404 reads the list
again. A draft that published the topology, was imported from it, or was
opened from its published diagram creates it again when it publishes: a source
config deleted since then passes the source freshness check.

## The builder-doc reference

`metadata.annotations["builder-doc"]` of a Topology config is a map with the
text sub-keys `digest`, `id` and `path`: each optional, at least one, no
other key. It is the one annotation that is not a string. Every JSON and
YAML encoding of a config shows it nested (REST, `phenix config get`/`edit`,
the Configs page). In memory (`store.Annotations` is still
`map[string]string`) and in BoltDB/etcd it is one compact JSON string; the
codec is on `store.Annotations` (`store.StructuredAnnotation`,
`Config.StoredJSON`). A string that is not a JSON object of these sub-keys
is refused (`POST`/`PUT /configs` answer 400).

```yaml
metadata:
  annotations:
    builder-doc:
      digest: sha256:1c92d3b0c95588fbb20926c1e58bb0452903c4e2cb0ffbf12fcdc19f72717732
      id: fd063e784604f43e5c39cc9959501cf117b4dc2be895e92f9bb98e23f8465c4b
      path: /phenix/topologies/pump-station/pump-station.builder.json
```

- `digest`: `sha256:` plus 64 lowercase hex, of the document's canonical JSON
  (`builder.Encode`: `json.MarshalIndent` with two spaces, struct field
  order, no final newline). It is not `sha256sum` of a file unless the file
  is byte for byte that encoding. Without `id` it also finds the stored
  document: `id = PublishedDocumentID(topology name, digest)`.
- `id`: a record in `builder.published`. It counts only when that record's
  `target` is this topology.
- `path`: a Builder file on the phenix server (see
  [Builder files](#builder-files)).

Go: `bapi.DocumentReference{Digest, ID, Path}`, `DecodeReference` (strict),
`EncodeReference` (canonical: sorted, compact), `StoredID(topology)`,
`Names(doc)` (`StoredID(doc.Target) == doc.ID` and digest equal when set),
`Publishes(topology, digest)`. Every reader goes through `Names`.

Resolution (`topologyDocument` in `web/builder_documents.go`): the stored
document the reference names for this topology wins; else, with a `path`,
the file, whose digest must equal `digest` when the reference has one (a
pin); else no document (`?topology=<name>` then says "No published Builder
document exists for topology <name>."). A store error or corrupt record
is an error and does not fall through to the file.

Publish and `phenix builder publish` write `digest` and `id` and keep an
existing `path`. Nothing writes a Builder file.

The Topology config hook (`api/builder/config_hook.go`, registered in every
phenix process, `--skip-validation` included)
on create and update:

- refuses a reference that does not decode (error matching
  `types.ErrValidationFailed` and `builder.ErrInvalid`; 400 over REST, and
  `phenix config create` logs `calling config hook: config validation
  failed: topology <name>: builder: invalid request: builder-doc...`).
  Messages: `builder-doc: digest is not a sha256 digest`,
  `builder-doc.path: must be an absolute path`, `... must be a clean path,
  with no ".", "..", "//" or trailing "/"`, `... must end in .json, .yaml or
  .yml` (case-sensitive); a path is at most 1024 bytes with no control
  characters. The config schema refuses a non-map (`value must be an
  object`), an unknown sub-key (`property "x" is unsupported`) and `{}`;
- when `id` and `digest` are both set and `id` is not the ID that digest
  gives for this topology name (a rename, or a copy under a new name), drops
  `id`, and `digest` too unless `path` is set (it stays as the file's pin);
  with nothing left the annotation is removed;
- re-encodes a valid reference canonically;
- never checks a path against the file system: confinement is checked by
  the process that reads the file, at read time.

An `id` without a `digest` cannot be checked there; readers find that it
names nothing. `phenix config edit` merges annotation maps, so it can change
a sub-key but not remove `builder-doc`; `PUT /configs` without the
annotation removes it. `${NAME}` in `path` is expanded when the config is
parsed, as in any config field.

The Configs page tags any topology with a `builder-doc` key `builder` and
sends its edit button to `/builder?topology=<name>`, whatever the
reference names.

## Builder files

The one exception to "Builder never reads server files": a Builder
document file named by `builder-doc.path`, read by
`bapi.ReadDocumentFile(root, excluded, path)` only when a diagram is
opened (`GET /builder/topologies/{topology}/document`), when a draft is
created from it, and when such a draft updates the topology. The listing,
the hooks and the CLI never read it. Nothing is cached.

Rules (all checked at read time, by `phenix ui`):

- below `--base-dir.phenix` (default `/phenix`) and not below the VM mount
  directory (`common.MountDir()`, default `<base>/mounts`), also after
  resolving symbolic links; opened through `os.OpenRoot`, so only a link
  with a relative target that stays below the root is followed: one that
  leaves the root is refused, and so is every link with an absolute target,
  also one that points below the root (`cannot be read`);
- a regular file (opened `O_NONBLOCK`; a FIFO or directory is refused), at
  most 5 MiB;
- one valid Builder document, JSON or YAML decided by content, not by the
  extension (which the path rule still requires). YAML goes through
  `builder.JSONFromYAML` (`types/builder/yaml.go`), which refuses anchors,
  aliases, merge keys, a second document, non-scalar or duplicate keys,
  other tags, `.inf` and `.nan`, and types scalars as js-yaml's
  `JSON_SCHEMA` does. No `${NAME}` expansion in the file;
- a `digest` beside `path` must equal the file document's digest.

Authorization is `configs` `get` on the topology in the URL, and nothing
else. The name scope of a role therefore does not protect a Builder file:
anyone with `configs` `create` or `update` on any one topology name, plus
`get` on it, can point that topology's `path` at any Builder file below the
base directory and read it. Stored documents have no such gap (their ID is
bound to the target). A forbidden or missing topology, no or an invalid
annotation, and a reference that names nothing all answer the same 404
(`builder document of topology <name> not found`).
File errors are a closed set (`bapi.DocumentFileError`), each a fixed
sentence naming the path and never anything the file holds; the cause is
not wrapped, and the log line is `builder document file not usable` with
`topology`, `path`, `reason`. A path that goes on through a regular file
(`ENOTDIR`) is missing, as a path nothing has is, so the answers do not say
which other names below the base directory are files:

| Status | `message` |
|---|---|
| 422 | `Builder file <path> is outside <root>, the directory phenix reads Builder files from.` |
| 404 | `Builder file <path> does not exist on this phenix server.` |
| 422 | `Builder file <path> cannot be read by phenix.` |
| 422 | `Builder file <path> is not a regular file.` |
| 413 | `Builder file <path> is larger than 5 MiB.` |
| 422 | `Builder file <path> is not a valid Builder document. Upload it in the Builder to see why.` |
| 422 | `Builder file <path> does not match the digest topology <name> records for it.` |

`topologyDiffers: true` on the read route (files only, omitted when false)
means the stored spec is not the document's projection for that topology
name (`bapi.TopologyHoldsDocument`; node order counts, so use the Topology
YAML download as the config's `spec`).

Edit as a draft of a file diagram: `POST /builder/drafts` with
`sourceToken: "builder-file/<topology>/<digest>"` (the digest the read route
returned) and the document exactly as returned. The server reads the file
again with the same authorization: the file errors above; 409 `The Builder
file of topology <name> changed since it was opened. Open its diagram
again.`; 409 `Topology <name> is no longer read from its Builder file. Open
its diagram again.` (a stored document now wins); 404 for a malformed token.
The UI reuses a user's draft with exactly that token, so a changed file
gives a new draft.

Publishing such a draft as an update of that topology is allowed while (1)
the draft's token names this topology and the file's digest still equals
the token's, or the draft's snapshot digest equals the file's, and (2) the
topology's spec still equals the file document's projection. Otherwise 409:
`topology <name> or its Builder file changed after this draft was opened
from the file`, or `topology <name> is not what its Builder file publishes,
so this draft cannot update it`. The token decides before the import rule
does: a file document that was imported from the topology (`source`) gives
its draft no other way to update it. The source freshness check still
applies to such a document, under any target name (`builder source
Topology/<name> changed after this draft was imported`). The client cannot
tell beforehand, so the dialog offers Update and shows the refusal. A
publish writes `digest` and
`id`, keeps `path`, never writes the file, and adds to `warnings`:
`Topology <name> names the Builder file <path>, which Publish does not
change. Download the diagram and replace the file to keep it in step.` From
then on the stored document wins and the listing row is `source: "store"`.
On a server without that record, the file is used only when it matches the
`digest`.

In the UI a file row has the local id `file/<topology>`, a text tag File,
"Read from <path>", and no Delete; the read-only banner says "You are
viewing the diagram of topology <t>, read from <path> on the phenix
server.", plus a sentence when `topologyDiffers`.

## Document metadata

A Builder document's root keys are, in this order, `$schema`, `revision`,
`metadata`, then the content: `nodes`, `networks`, `edges`, `viewport`,
`grid`, and the optional `scenarios`, `source`, `layout`, `templates` and
`icons`. `metadata` (`bdoc.Metadata`) is required and holds only `id`
(required, the document ID), `name`, `description`, the four provenance
fields below, and `notes`. Strict decoding refuses any other key there, and
refuses the metadata keys at the root (there is no shim for the old root
fields `id`, `name`, `description`, `author`, ...). A missing or null
`metadata` is reported as `metadata.id` "document ID is required"; issue
paths name the new place (`metadata.createdBy`, `metadata.notes[3]`). Go
(`validateMetadata` in `validate.go`) and JS (`validate.js`, `decode.js`)
check it alike through the shared corpus.

`metadata.notes` are free text about the diagram as a whole: at most 100
(`MaxDiagramNotes`, `MAX_DIAGRAM_NOTES`), each not blank after trimming, at
most 4096 bytes (`MaxDiagramNoteBytes`) and free of control characters but
newline and tab. They are document content (in the digest) and never
written to a config. The Inspector's Diagram view lists them after Scenarios
under Notes (`InspectorDiagram.vue`, testids `inspector-notes`,
`inspector-note-N`, `inspector-note-add`): a textarea "Note N" and a
"Delete note N" button each, "No notes." when there are none, and Add note,
disabled at 100 with a hint. A note is written when its textarea fires
`change`, through `store.setDiagramNotes(notes)` (model `setDiagramNotes`,
one undo step "Updated diagram notes"); blank rows are dropped. Text over
4096 UTF-8 bytes or with another control character is never written:
`diagramNoteProblem` (`model.js`) names why, the textarea gets
`aria-invalid` and an error under it (`role="alert"`, testid
`inspector-note-error-N`, named by `aria-describedby`), and the diagram
keeps that note as last saved until the text is fixed; model
`setDiagramNotes` returns the document unchanged for a list holding such a
note. "Blank" means only Go's `unicode.IsSpace` white space everywhere
(`isBlank` in `text.js`, `trimSpace` in `validate.js`, and `notePattern` in
`schema.go`, which spells the set out as `\x00-\x20\x7f\x85\p{Z}` since `\s`
differs between Go and ECMAScript): U+0085 is blank, U+FEFF is not. A
read-only draft shows the notes as text.

The generated schema (`builder.Schema()`, committed as
`src/js/src/builder/schema/builder-v1.schema.json`) gives every
Builder-owned property a `title`, a `description` and `examples`
(`documented()` in `schema.go`, values in `schema_examples.go`; the bundled
phenix `$defs` are left alone). `TestSchemaDocumentsEveryProperty` walks it in Go and
`schema-examples.test.js` checks each example against its subschema with
ajv. The Inspector does not show these titles: `schema.js` strips them from
the fields it builds, so labels stay as they were. `openapi.yml` lists the
root (`BuilderDocument`) and metadata (`BuilderDocumentMetadata`)
properties, which `TestBuilderDocumentDocumented` in `web/builder_test.go`
holds to the Go structs.

## Document provenance

A document's `metadata` has four optional string fields after
`description`: `createdBy`, `createdAt`, `updatedBy`, `updatedAt`. Users are
at most 256 bytes with no control characters; times are exactly
`YYYY-MM-DDTHH:MM:SSZ` (UTC, whole seconds); an empty string or null is
none; no pairing or ordering rule. Revision stays 1. They are document
content and part of its digest. `source.updatedAt` is a different field
(the imported config's time).

The server stamps them in `api/builder` when it stores a draft snapshot;
the editor never sets them:

- `POST /builder/drafts` (`CreateDraft`): `createdBy` and `createdAt` are
  the body's when present, else the caller and now; `updatedBy` and
  `updatedAt` are always the caller and now. The draft record keeps the two
  as `documentCreatedBy` and `documentCreatedAt` (a record with the old key
  `documentAuthor` is refused as corrupt). So an Upload keeps the file's
  `createdBy`, and anyone with `configs` `create` can name anyone. A body
  cannot set `updatedBy` or `updatedAt`, except through the unchanged copy
  below.
- Unchanged copy: with `sourceToken` `builder-doc/<id>` or
  `builder-file/<topology>/<digest>` and no `forkOf`, a body whose canonical
  JSON is the opened document is stored unstamped, so the draft's `digest`
  equals the document's and an unchanged publish answers topology
  `skipped`. A client must send the document exactly as `GET` returned it.
  For a `builder-file/` token all four fields are then whatever the file
  says, also in what the draft publishes before its first save, as in a
  document `phenix builder publish` stores: they are only as trustworthy as
  whoever can write the file.
- `POST .../snapshots` (`AppendSnapshot`): `createdBy` and `createdAt` come from
  the draft record (left out when it has none); `updatedBy` and `updatedAt`
  are the caller and now, equal to the snapshot's `createdBy` and its
  `createdAt` cut to seconds. Every save is an edit, also one that changes
  nothing.
- Nothing else writes a document: cursor moves (undo, redo, restore),
  snapshot delete, shares, publish, a file read and the CLI never stamp. A
  published document holds the fields of the snapshot it was published from.
- A value that is not valid in any of the four fields answers 422 on create
  and save.

Create and save responses have no document, so they carry `stamp:
{createdBy?, createdAt?, updatedBy?, updatedAt?}` (empty fields left out, `{}`
for an unchanged copy of a document that names nobody); the editor copies
it into its document (`withStamp` in `model.js`). No other response has
`stamp`. Copying it replaces `store.doc`, so the Inspector's watch on the
document skips a change of the stamp alone (`sameButStamp`): a reset there
drops text being typed in a field (`builder-inspector.spec.js` checks it).

`sourceFile` on `POST /builder/drafts` records the name of the uploaded
file a draft came from (the UI sends it for Upload of a file and Import of
a config file): a base name of at most 255 bytes, no `/` or `\`, not
`.` or `..`, no control characters, else 422. It is returned on every draft
response when set, is never used to open anything, and a `forkOf` draft
records none.

## CLI: phenix builder publish

```bash
phenix builder publish </path/to/document> [-n|--name N] [--update] [--dry-run] [--user U] [--record-path]
```

One Builder document file (JSON or YAML by content, at most 5 MiB, no
`${NAME}` expansion) becomes a Topology config with its stored published
document and a `builder-doc` of `digest` and `id`. It runs in the CLI
process against the store (`bapi.Service.PublishTopology` in
`api/builder/publish.go`, command in `cmd/builder.go`), with or without a
running `phenix ui`.

- Name: `--name`, else `bdoc.TopologyName(document name)`, the Publish
  dialog's proposal ("Pump station" gives `Pump-station`).
- It refuses what Publish refuses and lists every blocker, one per line.
- Existing topology: the same document already published there is a no-op
  (`topology already up to date`, exit 0). Otherwise `--update` is required
  (`topology X already exists; use --update to replace it`), and allowed
  only when the topology is still exactly what its stored document
  publishes, or the document's `source` names the topology with a
  `source.digest` equal to the stored topology's. Else: `topology X was
  changed after it was published, and replacing it would discard that
  change`, or `topology X was not published from a Builder document that is
  still stored, and this document was not made from the topology as it is
  now` (`generatedFrom`, which compares `bdoc.ImportDigest`, so a
  `builder-xml` topology is replaced only by a document imported from it as
  it is now, diagram included). An update of a `builder-xml` topology
  deletes the annotation (`ReplaceLegacyDiagram`) and warns. There is no
  force flag.
  An update keeps the topology's other annotations and an existing `path`.
- `--dry-run` runs every check, writes nothing, and prints a report on
  stdout (Document, File, Digest, Document ID, optional Path, Topology with
  "would be created/updated/left as it is", Nodes, Warnings). A refusal
  prints the error and exits 1. It is the way to get a file's digest.
- `--record-path` also writes `path`: the absolute, clean path of the input
  file (must pass the path rule). Outside the base directory or below the
  mount directory the command still succeeds and warns that the server does
  not read the file there. `--update --record-path` replaces a recorded
  path.
- The document is stored as it is in the file (no stamping). The record's
  `createdBy` is `--user`, else the sudo caller, else the OS account.
- Exit 0 for created, updated or up to date; 1 otherwise. Results and
  warnings are log lines on stderr (`topology created`, `topology updated`,
  `topology already up to date`, with `name`, `document`, `digest`).
- Not done: scenarios, experiments, VLAN aliases (warned), includes named by
  file path (warned, not checked), drafts. No broadcast to open pages, and
  no lock shared with a running server: a CLI publish and a UI publish of
  one topology at the same moment are not serialized.
- REST has no single "publish this document" call: create a draft, then
  publish it.

`phenix config create` recognizes a Builder document (`bdoc.IsDocumentText`):
found in a directory it is skipped with the log line `skipped Builder
document; use phenix builder publish`, and named on the command line it is
refused with `<file> is a Builder document, not a configuration: use
"phenix builder publish <file>" to create its topology`.

## Legacy import

The converter is pure Go in `src/go/types/builder/` (`legacy_xml.go` reads,
`legacy.go` converts): `DecodeLegacy(bytes)` → `*LegacyDiagram` or a
`*LegacyError{Reason, Message}` (reasons `not-diagram`, `malformed`,
`doctype`, `root`, `too-large`, `too-deep`, `too-many`); `FromLegacy(diagram,
name)` for a bare diagram; `FromLegacyTopology(config, options...)` for a
Topology with `builder-xml` (same options as `FromConfig`, so combine works);
`HasLegacyDiagram(config)`, `LegacyXMLAnnotation`. Both run `FromConfig`
first and lay the XML over it.

- Input: plain mxGraph XML (root `mxGraphModel` or `root`, UTF-8, no DOCTYPE
  or entity declaration, at most 5 MiB (`MaxLegacyBytes`), 32 levels, 10000
  cells), or a Topology config (YAML or JSON) whose `builder-xml` holds such
  XML. No base64, no compressed or `<mxfile>` draw.io form, no pasted text.
- Bare diagram: node settings come from each cell's `schemaVars`; `$NAME`
  placeholders are resolved from the diagram's `experimentVars` and the four
  legacy defaults (`DEFAULT_MEMORY` 2048, `DEFAULT_VCPU` 1,
  `DEFAULT_VM_IMAGE` `ubuntu.qc2`, `DEFAULT_ROUTER_IMAGE` `vyos.qc2`), never
  from the environment; values stay strings. `source.kind` is `manual`.
- Topology: its spec is the truth; the XML gives positions, VLAN ids (as
  VLAN aliases), notes (text cells) and groups (containers), matched by
  hostname ignoring case. Diagram-only nodes are left out, topology-only
  nodes placed below.
- Both: positions ×2 and snapped to 16; a switch is added for a VLAN drawn
  without one; a switch is named after its network; icon variants map to
  `router`, `firewall`, `desktop`, `server`, `external`; styles, layers,
  edge labels and the rest are dropped. The warnings are a closed list,
  built in `legacyNotes.sentences` (plus the unreadable-diagram warning in
  `FromLegacyTopology`); names are sorted, 8 shown, then "N more".
- `POST /builder/legacy` (`web/builder_legacy.go`): `{content, name?}`
  (strict; `name` names a bare diagram, default `legacy-diagram`), needs
  `configs` `get` and `create` (a Topology file is parsed as `POST /configs`
  parses one, `${NAME}` from the environment included). Answers `{document,
  warnings, source?}` (no `source` for a bare diagram), 200, no ETag;
  400/403/413/422 with sentences that repeat nothing of the content but a
  config's name and kind or the XML root name. Nothing is written.
- `POST /builder/generate` of a stored (or uploaded) Topology with
  `builder-xml` and no `builder-doc` converts through `(*builderAPI).generate`
  (`web/builder_sources.go`); `source.builder` is then `builder-xml`, and
  `source.digest` (`bdoc.ImportDigest`) covers the annotation, as does a
  sources row's `digest`.
- Draft tokens: Import of the stored topology `Topology/<name>`; Upload of a
  Topology file `uploaded/Topology/<name>`; Upload of a bare diagram
  `uploaded/legacy-xml` (`LEGACY_TOKEN`). Only the first can update the
  topology.
- Publish of that draft to the topology (web, and `phenix builder publish
  --update`) calls `bapi.ReplaceLegacyDiagram`: deletes `builder-xml`, keeps
  the other annotations, and warns `The legacy Builder diagram of topology
  <name> was replaced by this diagram.` (or `… could not be read and was
  removed.` when `DecodeLegacy` refuses it). While a topology has
  `builder-xml`, `topologyUpdateMatchesSource` accepts only a draft whose
  document source is that topology (not one imported from an experiment);
  the client mirrors it (`updateBlocker`, `legacyDiagramUpdate` and
  `targetHint` in `publish.js`). A changed topology or diagram is the usual
  409 `changed after this draft was imported`.
- UI: Upload's fourth source "Legacy Builder diagram or Topology"
  (`LEGACY_SOURCE` in `dialogs/message.js`), a file field only
  (`upload-legacy-file`), submit "Convert"; `store.convertLegacy({content,
  name})` checks the document and the dialog loads it on Continue, so Cancel
  keeps the open draft. XML given to File or Paste text gets the hint
  `legacyDiagramHint`. The Import list labels a legacy topology "NAME (legacy
  Builder diagram)" with the hint `import-legacy-hint`. Warnings show in the
  shared `dialogs/ImportWarnings.vue`. The palette finds Upload and Import
  by `legacy`.
- No CLI, no bulk conversion, nothing at server start.

## Drafts page

Tabs in order: My Drafts, Shared Drafts, Published Diagrams, Node Templates
(the view's `templates` prop and slot of `BuilderDrafts`), Other users'
drafts (shown when it lists something). A card shows `Owner: …` on one line
and `Updated …` on the next; Open, Share, Delete, Publish and Exp are one
size. Publish on a card (`draft-publish-<id>`, own drafts, edit shares, and
other users' drafts the role may change) loads the draft into the store
without the editor (`store.loadDraft(owner, id, {quiet: true})`) and opens
the editor's Publish dialog over the drafts page, titled `Publish <name>`.
Exp (`published-experiment-<id>` on a published row, and in the editor
toolbar after Share; `store.experiment`, `experimentName`) navigates to the
experiment in the same tab; palette command "Open experiment".

Bulk actions are a client loop over the per-item routes (`builder/bulk.js`
`runBulk`, `listSelection.js`, `BuilderBulkBar.vue`, `BuilderBulkSummary.vue`,
`dialogs/BulkShareDialog.vue`): at most 4 requests at once (topology deletes
1), one retry on a 412 using the `ETag` the 412 carries. Rows: Select all,
"{n} of {m} selected", Share selected (My Drafts, with sign-in), Delete
selected (My Drafts, Other users' drafts with `canDelete`, Published
Diagrams stored rows). Bulk Share merges the chosen users and access into
each draft's list; a draft that would pass 25 users fails. A session that
ends stops the run ("Not attempted.").

## Presentation fields and custom icons

Optional document fields (schema revision stays 1; none is ever written to
a Topology, Scenario or Experiment config, `TestToTopologyOmitsPresentationFields`):
device and switch `outlineColor`, `fillColor` (`#rrggbb`); switch `notes` (the
rules of `metadata.notes`; a device's notes are its spec's `general.notes`,
which are published); network and edge
`lineStyle` (`solid`, `dashed`, `dotted`, `dash-dot`; empty is Auto); group
`description`, `borderStyle` (`solid`, `dashed`, `dotted`, `double`),
`iconKey`, `icon`; device `icon`; root `icons` (`{"sha256:<hex>": {name?,
data}}`, at most 50 (`MaxDocumentIcons`, `MAX_DOCUMENT_ICONS`), each a PNG of 1 to 96 pixels a side and at most 40960
bytes, chunks `IHDR`, `PLTE`, `tRNS`, `IDAT`, `IEND` only, key = SHA-256 of
the bytes; `bdoc.ValidateIcons`, `customicons.go`) and root `templates` (at
most 50; see [Node templates](#node-templates)). Go and JS validate them
alike (`testdata/validation-corpus.json`). The Inspector writes them; the
network's own `color` is labelled "Edge Color".

Drawings: node kinds `shape` (`shape: {shape: rectangle|circle, label?,
fillColor?, outlineColor?, borderStyle?}`), `icon` (`icon: {iconKey?, icon?,
label?}`, exactly one of a built-in key and a custom icon) and `line`
(`line: {points, label?, color?, lineStyle?, startArrow?, endArrow?}`, 2 to
64 points (`MinLinePoints`, `MaxLinePoints`) relative to the node's
position, which the editor keeps at the top left of the points' box with
the box as `size`: `placedLine` in `model.js`). Colors are `#rrggbb`, styles
the border and line styles above; the payload's `label` is the node's
label. Like notes and groups they never reach a config
(`TestToTopologyOmitsVisualNodes`), GEXF or Topology YAML, take no
connections, may have a group `parentId`, and Go and JS check them through
the shared corpus. The schema's payload definitions are `shape`, `iconNode`
(the node key is `icon`; `$defs.icon` is a custom icon) and `line`. The
palette (`PALETTE` in `catalog.js`, entries `rectangle`, `circle`, `icon`,
`line`; testids `palette-<id>`) and the commands `add.rectangle`,
`add.circle`, `add.icon`, `add.line` add them. Canvas: `nodes/ShapeNode.vue`,
`IconNode.vue`, `LineNode.vue` (flow types `builderShape`, `builderIcon`,
`builderLine`; shapes and lines on the groups' layer, under devices). The
canvas turns Vue Flow's `elevateNodesOnSelect` off and stacks nodes itself
(`nodeZIndex` in `adapters/vueflow.js`, applied by `toFlowNodes` and
`withSelection`): a selected node, and every node in a selected group, is
lifted by 1000, but a shape or a line never is, so a selected one never
covers a device, a switch or their connection handles. A selected line has
a handle per point (`line-point-N`, pointer only and `aria-hidden`;
dragging commits one `store.setLinePoints`, double-click on the line
`store.insertLinePoint`, and on a clicked handle the keys of
`linePointKey` in `model.js`: arrows `moveLinePoint`, Delete and Backspace
`removeLinePoint`, which keeps two points, Escape back to the line); the
Inspector's Points list (absolute canvas coordinates) is the keyboard path:
X and Y, Add, Remove, and "Insert point after point N"
(`insertedListItem` in `adapters/forms.js`, offered through
`INSPECTOR_INSERT_ITEM` to `InspectorArrayRenderer.vue`: a bend halfway to
the next point, in the working copy that Apply commits as one step).
Layouts leave drawings where they are, moving one in a group with its
group, and then grow each group right and down to hold its drawings with
the layouts' `GROUP_PADDING`, innermost first (`runLayout` and
`growAroundDrawings` in `layouts/index.js`). Notes, groups, shapes and
icons resize with `@vue-flow/node-resizer` (`nodes/NodeResize.vue`,
committed by `store.resizeNodeBox`, which keeps a group around its
members) and with Alt+Shift and an arrow key (`keyResizedSize`,
`minimumSize`, `RESIZABLE_KINDS` in `model.js`). The resizer's handles
(`.builder-resizer[data-node-id]`) and the line's handles are in Vue
Flow's edge label layer (z-index 2000, over every node), outside the
node's wrapper (a button, which holds nothing focusable); `NodeResize.vue`
and `LineNode.vue` reach the store through what `BuilderCanvas.vue`
provides (`nodes/canvasEditing.js`), so a node rendered alone imports no
store. PNG and SVG downloads keep a line's arrowheads: `exportCopy` in
`exporters.js` removes the copy's IDs but renames each one an SVG
`marker-start`, `marker-mid` or `marker-end` points at (`<id>-image-<n>`),
with the references.

A device node shows `spec.type` as stored ("External" when `spec.external`,
"Device" without a type). Devices and switches have an info tooltip on
hover (400 ms) and on keyboard focus (`BuilderNodeTooltip.vue`,
`nodeInfo.js`, `useFixedTooltip`); it is not in PNG or SVG downloads.

Custom icons: a document keeps its own copy of each icon it uses, so it
opens anywhere; the browser converts PNG, JPEG, GIF, WebP and SVG (up to
5 MiB) to a PNG of at most 96 pixels before upload. Icon bytes are drawn
only through `BuilderIcon`'s `<img>` with a `data:image/png;base64,` URL
built by `iconSrc` in `icons.js`, never as markup. `store.iconShelf` holds
icons the open diagram may take in; `settleIcons(doc, known)` in
`store.commit` adds used icons and drops unused ones. The per-user icon
library: `GET/POST /builder/icons`, `DELETE /builder/icons/{hex}` (`configs`
`list`/`create`/`delete`; own library only, no owner in the path); a PNG of
at most 96 pixels and 65536 bytes (body 131072); a strict PNG of at most
40960 bytes is kept as it is, any other is re-encoded to its pixels; 64
icons and 1 MiB per user; 201 for a new icon, 200 for bytes already held;
no rename. Records: `builder.icons`, key `<OwnerScope(user)>/<hex>`
(`api/builder/icons.go`, `scope.go`). Every `/builder/` response has
`X-Content-Type-Options: nosniff`; the icon routes also
`Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`
(`builderResponseHeaders`). No CSP is set on the application page. Files:
`icons.js`, `iconLibrary.js`, `dialogs/IconDialog.vue`,
`inspector/InspectorIconControl.vue`, `nodes/nodeColors.js`.

## Node templates

Two stores. Diagram templates live in the document (`templates`: `{id,
name, description, device: {iconKey?, icon?, outlineColor?, fillColor?,
spec}}`; name 1 to 128 bytes, description at most 1024, both one line;
device at most 16384 bytes as JSON; `bdoc.Template`, `template.go`); they
travel with downloads, shares and publishes, and are never written to a
config. The palette's "+" (New device template) and the template editor
(`dialogs/TemplateDialog.vue`, the Inspector's form code through
`templateEditorHost` in `templates.js`) write them. `TemplateDevice` must
hold every `Device` field but `hostname`, `interfaces` and `includedFrom`
(a reflection test pins it), so a new device field is a template field too.
A device made from a template (`nodeOptionsFromTemplate`) keeps no link to
it.

The per-user library (`api/builder/templates.go`, `web/builder_templates.go`):
one record per user in `builder.templates` under `lib/<OwnerScope(user)>`
(`LibraryKey`), at most 512 KiB, holding templates, collections and the
custom icons they name (`icons`). A user with no record has the five
built-in templates at version 1; the first change stores them, and a
deleted one never comes back. Limits: 200 templates, 50 collections, 200
templates per collection, 50 icons, 25 users per item. Every write is
`Service.UpdateLibrary(ctx, owner, actor, change)`: read, run `change`,
drop unused icons, stamp versions and times, validate, write against the
read revision; up to 5 tries, then `ErrBusy` (503, `Retry-After: 1`).
Refusals are `*LibraryError`. Templates and collections have strong ETags
`"<version>"`; only the two PUT routes need `If-Match`. A record a newer
phenix wrote is `damaged`: listed with no own items, and every change 409.
Sharing (`/share`, `configs` `update` and a user account) and server-wide
publishing (`/publish`, also `builder-templates` `publish`; taking back:
the owner, or anyone with that permission) give recipients a live, read-only
view and add hint records `in/<OwnerScope(recipient)>/<OwnerScope(owner)>`
and `pub/<OwnerScope(owner)>` (value `{}`, never removed), so a listing
reads only the libraries hints name. A share is bound to the recipient's
account (stale after it is deleted or recreated). With authentication off
the one library is `global-admin`'s and sharing is unavailable. The
start-of-server cleanup never touches `builder.icons` or
`builder.templates`. UI: the Node Templates tab (`BuilderTemplates.vue`,
`CollectionDialog.vue`, `TemplateShareDialog.vue`), palette groups "This
diagram", "My library", "Shared with me", "Server-wide", "Built-in"
(`paletteTemplateGroups`, `TEMPLATE_GROUP_LABELS`; a group heading shows only
with two groups or more), the library button "Node Templates library",
`store.templates` and its actions (`fetchTemplates`,
`createLibraryTemplates`, `updateLibraryTemplate`, `createCollection`,
`updateCollection`, `deleteLibraryItems`, `shareLibraryItems`,
`publishLibraryItems`).

No `phenix` command converts legacy diagrams, imports configs, or manages
the icon or template library; they are web UI and REST only.

## Routes

All routes are relative to `/api/v1`.

| Route | Purpose |
|---|---|
| `GET /schemas/builder/v1` | JSON Schema of the Builder document (`builder/v1`); needs `schemas` `get` on the resource name `builder` |
| `GET/POST /builder/drafts` | List the caller's drafts (`drafts`), other users' drafts the caller may see (`shared`) and unreadable drafts (`damaged`); create a draft (optionally `forkOf` or `sourceToken`) |
| `GET/DELETE /builder/drafts/{owner}/{draft}` | Read a draft with its current document; delete it |
| `GET/POST /builder/drafts/{owner}/{draft}/snapshots` | List or append snapshots (append needs `If-Match`) |
| `GET /builder/drafts/{owner}/{draft}/snapshots/{snapshot\|current}` | Read one snapshot's document |
| `DELETE /builder/drafts/{owner}/{draft}/snapshots/{snapshot}` | Delete a version other than the current one (needs `If-Match`) |
| `PATCH/PUT /builder/drafts/{owner}/{draft}/cursor` | Undo and redo: move the draft's current snapshot |
| `POST /builder/drafts/{owner}/{draft}/publish` | Create or update the topology and experiment configs, and add the topology to the document's scenarios |
| `GET/PUT /builder/drafts/{owner}/{draft}/shares` | Read or replace who a draft is shared with (owner only) |
| `GET /builder/drafts/{owner}/{draft}/shares/candidates` | Every account that can receive a share of the draft |
| `GET /builder/sources` | Configs a document can be generated from or publish to; topology rows have `includeCount` |
| `POST /builder/generate` | Build a document from a stored or uploaded Topology or Experiment, with `includes`, `copy`, `name`; converts a `builder-xml` topology (`configs` `get`; `create` for `content`) |
| `POST /builder/legacy` | Convert a legacy diagram or a Topology file with `builder-xml` into a document (`configs` `get` and `create`; nothing is written) |
| `POST /builder/export/topology` | The Topology config a document publishes as, as YAML, with `warnings` and `publishBlockers` (nothing is written; needs `configs` `get`) |
| `GET /builder/documents[/{document}]` | Published Builder documents (`source: "store"`); the listing also has a row per topology read from a Builder file (`source: "file"`) |
| `DELETE /builder/documents/{document}` | Delete the topology a published document is current for, and the topology's published documents |
| `GET /builder/topologies/{topology}/document` | The document a topology's `builder-doc` names, stored or read from its Builder file: the listing row plus `digest`, `size`, `document`, and for a file `topologyDiffers` |
| `GET/POST /builder/icons`, `DELETE /builder/icons/{icon}` | The caller's icon library (`configs` `list`, `create`, `delete`) |
| `GET /builder/templates` | Templates and collections the caller can use: own (`source` `own`), shared (`shared`), server-wide (`server`), with their `icons`, `canShare`, `canPublish`, `damaged`, `limits` (`configs` `list`) |
| `GET /builder/templates/candidates` | Accounts the caller's items can be shared with (`configs` `update`, a user account) |
| `POST /builder/templates/{owner}/items`, `PUT …/items/{template}` | Add templates (optionally as a new `collection`), replace one (`If-Match`) (`configs` `create`, `update`; owner only) |
| `POST /builder/templates/{owner}/collections`, `PUT …/collections/{collection}` | Add, replace a collection (`If-Match` on PUT) (`configs` `create`, `update`; owner only) |
| `POST /builder/templates/{owner}/delete` | Delete templates and collections (`configs` `delete`; owner only) |
| `POST /builder/templates/{owner}/share` | Add or remove users of items (`configs` `update`, owner, a user account) |
| `POST /builder/templates/{owner}/publish` | `serverWide` true publishes the owner's items (`configs` `update` and `builder-templates` `publish`); false takes them back (owner, or anyone with `builder-templates` `publish`) |

The OpenAPI document served at `/docs/` describes every request and response.

## Storage

Drafts live in the phenix store as records, apart from configs
(`builder.drafts`, `builder.chunks`, `builder.published`; the startup cleanup
lists only these three). The icon and template libraries are records in
`builder.icons` and `builder.templates`, keyed through `OwnerScope(user)`
(the lowercase hex SHA-256 of the user name, `api/builder/scope.go`): a
library follows the user name, as draft ownership does, and nothing removes
it when an account is deleted. With an etcd
store, phenix compacts etcd's history for the whole cluster (see
`compaction-retention` in [cli.md](./cli.md)). When etcd reaches its space quota,
any write it refuses, including config writes (`POST`/`PUT /configs`) and
Builder saves, answers `507` with `etcd is out of space: ...` as `message`
and an empty `cause`; the editor keeps the changes queued in the browser and
retries. Publish reports it in its `partial` result as `<stage> publication
failed: etcd is out of space: ...`. The error is logged once per request
(`store.ErrNoSpace`; the OpenAPI response is `InsufficientStorage`).

## Working on Builder code

Read this section before changing any file listed below.

### Where the code is

| Area | Files |
|---|---|
| Document model, generation, publishing to configs, validation, JSON Schema, YAML reading | `src/go/types/builder/` (`document.go`, `generate.go`, `detach.go` (combine, copy), `topology.go`, `validate.go`, `schema.go`, `yaml.go`, `customicons.go`, `template.go`, `legacy_xml.go` and `legacy.go` (legacy conversion)) |
| Drafts, snapshots, sharing, published documents, libraries, limits | `src/go/api/builder/` (`service.go`, `shares.go`, `published.go`, `chunks.go`, `limits.go`, `validate.go`, `icons.go`, `templates.go`, `scope.go`; `config_hook.go` checks a topology's `builder-doc` and removes a deleted or renamed topology's documents; `file.go` reads Builder files; `publish.go` publishes a document as a topology for the CLI, and `ReplaceLegacyDiagram`) |
| Built-in Builder role and its start-up check | `src/go/api/config/default/builder.yml`, `src/go/web/rbac/migrations.go` (`EnsureBuilderTemplatesPublishPermission`), `src/go/web/init.go` |
| `builder-doc` codec (nested in JSON and YAML, a string in memory and in the store) | `src/go/store/types.go` |
| `phenix builder publish`, and `phenix config create` recognizing Builder documents | `src/go/cmd/builder.go`, `src/go/cmd/config.go` |
| Record store for drafts (BoltDB and etcd, etcd compaction) | `src/go/store/*record*.go`, `src/go/store/etcd_record_compact.go` |
| HTTP routes, authorization and RBAC | `src/go/web/builder*.go` (`builder.go` holds the authorization model, the routes and the response headers; `builder_legacy.go`, `builder_icons.go`, `builder_templates.go`, `builder_experiments.go` (the `experiment` link); `builder_assets.go` serves the editor's files, which `src/js/plugins/builder-assets.js` compresses in the UI build) |
| Editor page and drafts landing | `src/js/src/views/Builder.vue`, `src/js/src/components/builder/BuilderDrafts.vue`, `BuilderTemplates.vue` (the Node Templates tab), `BuilderBulkBar.vue`, `BuilderBulkSummary.vue`, `BuilderHeaderButtons.vue` (the buttons both headers share) |
| Configs page links | `src/js/src/components/configs/ConfigsList.vue`, `ConfigsEditor.vue`, `src/js/src/builder/configs.js` |
| Editor components | `src/js/src/components/builder/` (canvas, Inspector, outline, toolbar, side columns, `BuilderSignIn.vue`, dialogs, nodes, edges) |
| Editor state and logic | `src/js/src/builder/` (`store.js`, `model.js`, `autosave.js`, `idb.js`, `tabs.js`, `session.js`, `signin.js`, `panes.js`, `commands.js`, `keymap.js`, `layouts/`, `adapters/`, `publish.js`, `templates.js`, `icons.js`, `iconLibrary.js`, `bulk.js`, `listSelection.js`, `nodeInfo.js`, `nodeNotes.js`, `grouping.js`, `groupingWorker.js`) |
| Generated schema bundle | `src/js/src/builder/schema/builder-v1.schema.json` |

### Rules

- Run `make generate` (or `make generate-builder-schema`) in `src/go` after
  changing `types/builder/` or the config schemas in `types/version/schemas/`:
  it rewrites the committed schema bundle above, and CI fails when the bundle
  is stale.
- A change to a route updates `src/go/web/public/docs/openapi.yml`; a Go test
  checks that every Builder route is documented.
- Keep the editor keyboard and screen reader accessible (WCAG 2.2 AA): focus
  never falls to `<body>`, dialogs return focus to their opener, and changes
  are announced through the shared live region.
- The server serves the files only Builder loads compressed, with their
  `Content-Length`, `Cache-Control: public, max-age=31536000, immutable` and
  `Vary: Accept-Encoding`: Brotli or gzip, as the request's `Accept-Encoding`
  weighs them (Brotli on a tie), else uncompressed. The UI build compresses
  them: the plugin in `src/js/plugins/builder-assets.js` starts from the
  `src/views/Builder.vue` chunk, leaves out files the rest of the UI also
  loads, writes each file's `.br` and `.gz` copies next to it when they are
  smaller, and lists the files in `builder-assets.json` beside
  `index.html`. The server sends nothing else compressed and answers a
  request for a copy itself with 404. Renaming or moving `Builder.vue`
  means updating the `view` passed to the plugin in `src/js/vite.config.js`;
  the build fails when it finds no chunk for it. Without the list, every file
  is served uncompressed and the server logs a warning at startup.
- The rest of the UI loads `src/builder/api.js` to send saves left unsent, so
  what it imports from `src/builder/` lands in a chunk the server sends
  uncompressed. Keep the decoder and validator out of it: the document size
  limit it shares with `decode.js` lives in `limits.js`, which imports
  nothing. A test in `api.test.js` checks this.
- In the UI code, a name follows the button's label, and a conversion keeps
  the server's word. Import (a draft from a Topology or Experiment config,
  stored or in a config file) is `dialogs/ImportDialog.vue`, dialog key
  `import`, ids and test ids `import-…`; it calls `store.generate`
  (`POST /builder/generate`). Upload (a Builder document you have, a
  published diagram, or a legacy diagram, which `store.convertLegacy` sends
  to `POST /builder/legacy`) is `dialogs/UploadDialog.vue`, `upload`,
  `upload-…`. Download (the open
  diagram as a file) is `dialogs/DownloadDialog.vue`, `download`,
  `download-…`; its files come from `exporters.js`, `gexf.js` and
  `store.exportTopology` (`POST /builder/export/topology`). Import and
  Upload both have a file, an error and a submit control, so a test finds
  the dialog by its title before it uses an id.
- Keep this file current when Builder behavior changes.

### Tests

- Go: `go test ./api/builder ./types/builder ./store ./web ./web/rbac` from
  `src/go`. The legacy converter has golden files under
  `types/builder/testdata/legacy/` (rewrite with `-update-legacy-golden`) and
  a fuzz test, `go test -fuzz=FuzzDecodeLegacy -fuzztime=30s ./types/builder/`.
  The `api/builder` and `web` tests share one in-memory record store,
  `src/go/store/recordtest/memrecord`, with the hooks `BeforeCreate`,
  `BeforeUpdate`, `AfterWrite`, `FailDelete`, `FailPrefixDelete` and `Stamp`.
  Tests that edit stored records use its methods (`Count`, `Keys`,
  `SetValue`, `Drop`, `RewriteLocked`), not its fields. It is a package of its
  own because the `store` tests import `recordtest`.
- Unit: `npx vitest run test/builder` from `src/js`.
- Browser: the `builder*.spec.js` Playwright specs in `src/js/e2e/tests/` need a
  running server. `builder-legacy.spec.js` (conversion, with
  `fixtures/legacy-sample.xml`), `builder-drafts.spec.js` (cards, card
  Publish, bulk actions), `builder-presentation.spec.js` (node face,
  tooltips, colors, line styles, icons) and `builder-templates.spec.js` run
  on the default server; `builder-sharing-templates.spec.js` runs with the
  sharing suite. With authentication off every test is `global-admin` on one
  shared library: a test selects and deletes only what it made, never
  changes the five built-in templates, and deletes any icon it uploads.
  `builder-sharing.spec.js` (sharing, and signing in again) needs a server
  with sign-in on (a JWT signing key and an admin user) and `E2E_SHARING=1`.
  `builder-files.spec.js` (topologies that name a Builder file by
  `builder-doc.path`) writes its files below the server's base directory, so
  it needs the server on the same machine and `E2E_BASE_DIR` set to the
  server's `--base-dir.phenix`; without it the spec skips.
  `src/js/e2e/README.md` has the setup, the `@known-defect`, `@cross-browser`
  and `@axe` tags, the Playwright projects, and how CI splits the suite into
  parallel jobs. Tag `@axe` only on full axe scans: CI runs them in a job of
  their own, apart from the smoke shards.
- A spec that does not test what screen readers hear may shorten the live
  region's 750 ms hold with `test.use({ announceHold: 100 })`, which sets
  `window.__BUILDER_ANNOUNCE_HOLD_MS__` for `BuilderLiveRegion.vue`. The
  inspector and editing specs do; `builder-a11y.spec.js` keeps the real hold
  and checks that messages arriving during it are joined. Specs assert
  announcements with `expect(builder).toHaveAnnounced(text)`, which waits for
  the message in a log of all the live region has shown, not with the
  region's current text, which the next message can replace before a slow
  runner reads it. Shared e2e helpers (`waitForApi`, `contrast`,
  `persisted`, `expectCounts`, `nextSnapshot`) are in `builder-support.js`.
- CI runs the e2e suite on Linux, where key names differ from macOS (Enter,
  not Return), and on slower machines.
