# Builder v2

Builder v2 is phenix's web topology editor, built on Vue Flow. It is a beta
behind the `builder-v2` UI feature. This file holds everything about it
that the main phenix skill leaves out.

**Read this file when** a task involves Builder v2: its drafts, sharing,
publishing, import, export or generation, its `/api/v1/builder-v2/*` or
`/schemas/builder-v2/v1` routes, the `builder-drafts` RBAC resource, the Builder
document format (`builder/v1`), the `builder-doc` annotation, Builder files
named by `builder-doc.path`, `phenix builder publish`, or any Builder v2 code
(see
[Working on Builder v2 code](#working-on-builder-v2-code)). The legacy
Builder (`/builder`, `builder-xml` topologies) is a different editor, covered
by [builder.md](./builder.md).

## Enabling it

`phenix ui --features builder-v2` enables Builder v2, the Vue Flow
topology editor (a beta), at `/builder-v2` and its draft/document APIs. It
leaves the legacy `/builder` route available for `builder-xml` topologies.
Its one CLI command is `phenix builder publish`, which makes a Topology from
a Builder document file and needs neither the feature nor a running server
(see [CLI: phenix builder publish](#cli-phenix-builder-publish)); drafts,
sharing and everything else are in the REST API and the web UI only.
Drafts autosave separately from phenix configs; in the web UI and REST API
only the explicit Publish action creates or updates topology, scenario, or
experiment configs. Its Router and
Firewall device templates create `minirouter` nodes (image `minirouter.qc2`)
of type `Router` and `Firewall`, which the `vrouter` app configures. The
Inspector suggests drive images, and the diagram checks flag a missing one,
from `GET /disks`; without the `disks` `list` permission it does neither.

Features are disabled by default and require restarting `phenix ui` after
changing `ui.features`.

## In the browser

Builder v2 works over plain HTTP as well as HTTPS. A published diagram
opens read only; Edit as a draft creates a draft from it (or reopens the draft
made from it before), which needs `configs` `create`, so a role with only
`list`/`get` can view drafts and published diagrams but cannot create, import,
upload, or publish. Configs' edit button for a Builder v2 topology links to
`/builder-v2?topology=<name>`, which opens the user's draft of it (making one
the first time) and then names it as `?draft=<owner>/<id>`, so a reload reopens
that draft; with the feature off, Configs explains that the topology can only
be edited in Builder v2. The Inspector also edits a node's labels,
annotations, and advanced (minimega `vm config`) settings. An interface's kind
picker offers Static or OSPF, DHCP or manual, and Serial; the Protocol and Type
fields under it hold the rest. With nothing
selected, its Diagram section shows two more parts below Name and
Description. Annotations lists the annotations of the config the diagram was
imported from, sorted by key and without `builder-` ones, under "From <Kind>
<name>, imported <time>"; a diagram drawn in the editor has no such part. A
value too long for its box scrolls in it, and Tab reaches the box only while
it scrolls. Above Annotations, a read-only Details block shows the document's
provenance (see [Document provenance](#document-provenance)): "Created
<time> by <author>", "Last edited <time> by <updatedBy>", and "Source file
<name>" when the draft record has `sourceFile`; a row without a value is
left out, and the block when no row is left. Scenario says whether the
scenario is stored or uploaded and lists
each of its apps with the hosts it runs on. A stored scenario's apps are read
with `GET /configs/Scenario/<name>`, which needs `configs` `get`; otherwise the
Inspector says it cannot read them. Edit scenario (Add scenario when there is
none) opens the same Scenario dialog as the toolbar's Scenario button; a
read-only draft shows neither. Leaving a draft,
publishing, or exporting first saves Inspector changes that were not applied,
as a draft snapshot with the summary `Saved unapplied changes to <node>`.
Logging out removes Builder v2's local drafts (IndexedDB `phenix-builder`)
and recent commands from the browser, as does signing in as a different user
(`phenix.builder.user` names whose data the browser holds), and keeps its
preferences (`phenix.builder.theme`, `phenix.builder.panes`,
`phenix.builder.minimap`, `phenix.builder.shortcuts` and
`phenix.builder.settings` in localStorage).
Logging out first sends changes still queued in the browser; if some remain,
a warning offers Export (one file per draft), Stay signed in (not once the
token has expired) and Log out anyway. The idle timeout and an expired token
show it for one minute (an expired token's only while the tab is visible),
then log out; for an automatic logout of an expired token on the Builder page
it also offers Sign in again. A draft keeps in its document's `layout` the
layout that last laid it out: the one chosen in the toolbar's layout menu, or
the one Auto layout or Auto-group ran. The layout menu names it, or says
Default, with no layout checked, for a draft without one (imported, uploaded,
blank or placed by hand). On such a draft, Auto layout and Auto-group run the
Settings layout ("Layout for drafts without one") and the draft then keeps
it. A run that moves nothing keeps no layout. Undo and Restore previous layout
bring Default back. A document may also hold each connection's `route` as a
layout drew it; publishing and export ignore both. ELK layered lays each
network out as a cluster and places the clusters in layers along the
connections between them, cutting a tall layer into slices that wrap into
columns, so connections run left to right, except one ELK reverses to break a
cycle between networks.

When the server refuses the session (a `401` on a save, a listing or a
publish), or the token expires while Builder v2 is open, a Sign in again
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
state still says to Export and sign in again.

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
toolbar has Draft History right after Minimap, then the save state, which is text the
toolbar's arrow keys pass by. Focus
mode (⇧⌘F or Ctrl+Shift+F) works on both views and stays on between them,
until the user turns it off or leaves Builder v2.

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
named users as `view` or `edit`. `GET /builder-v2/drafts/{owner}/{draft}/shares`
lists them (owner only). `PUT` of the same path with `If-Match: "shares-N"` and
`{"shares":[{"user":"bob","access":"edit"}]}` replaces the list: at most 25
users, and unknown users, the owner and duplicates get `422` with per-user
`errors`. A share gives view (`get`/`list`) or edit (also save, undo and
publish under the recipient's own permissions). It never gives delete or
sharing. Recipients still need their `configs` permissions. A share is bound
to the recipient's account, so a user deleted and recreated under the same
name loses it. `GET /builder-v2/drafts/{owner}/{draft}/shares/candidates` lists
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

Every Builder v2 request also needs the base `configs` permission of the verb
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
`GET /builder-v2/drafts` lists other users' drafts the caller may see in `shared`,
those shared with the caller with `via: "share"`. Changing shares changes the
draft's ETag; the share list has its own `"shares-N"` tag. A draft that has
ever been shared shows as damaged (its owner can delete it) on a phenix
version without sharing. `GET /builder-v2/drafts` also returns `damaged`: drafts
whose metadata this server can no longer read (written by a newer phenix, say),
with `id`, `owner`, `etag`, `canDelete`, and `title` and `updated` when they
can be read. They cannot be opened, only deleted with that `etag`; a `412` on
`DELETE` also carries the current `ETag`.

Publishing still requires the applicable config, scenario, and experiment
permissions; Builder draft access does not bypass them.

## Saving: snapshots, ETags and the local queue

A snapshot append (`POST /builder-v2/drafts/{owner}/{draft}/snapshots`) may carry
`opId`, the client's id for the save (1-128 letters, digits, `.`, `-`, `_`,
starting with a letter or digit). The snapshot manifest keeps it, and snapshot
listings return it, so a client whose response was lost can tell the snapshot
was stored. A draft keeps at most 50 snapshots and 50 MiB of them; past either,
the oldest are dropped. A last publication naming a dropped snapshot is kept,
as when that snapshot is deleted: the draft is then dirty, and it (or a draft
that forks it) can still update the topology or experiment it published. The
UI keeps unsaved Builder v2 edits in the browser's `phenix-builder` IndexedDB
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
`DELETE /builder-v2/drafts/{owner}/{draft}/snapshots/{snapshot}` with `If-Match`
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

`GET /builder-v2/sources` groups configs by kind: `topologies` and `experiments`
(what a document can be generated from, reported as `generatable: true`),
`scenarios` (selectable when publishing) and `images` (node property editing).
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
rest. `POST /builder-v2/generate` also sets `source.importedAt` (RFC 3339, UTC).

`POST /builder-v2/generate` accepts either `{"source":"Topology/name"}` (or an
Experiment source) or `{"content":"..."}` containing an uploaded JSON/YAML
Topology or Experiment. Uploaded sources are reported as `stored: false` and
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
file Builder v2 reads is a Builder file named by `builder-doc.path`; see
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

A stored scenario reference carries the config's `apiVersion` and content
`digest` as `GET /builder-v2/sources` lists them, never its content. Generating
from a stored Experiment whose `scenario` annotation names a Scenario the
caller may list produces such a reference, so the draft publishes back with
scenario action `use`. If that Scenario is missing or hidden from the caller,
or the Experiment was uploaded, the Experiment's embedded copy is attached as
an uploaded scenario of the same name, with a warning: an uploaded Experiment
is never bound to this server's Scenario of that name, which may differ.

Publishing an uploaded Scenario with action `update` requires
`scenario.expectedDigest`, copied from a fresh matching entry returned by
`GET /builder-v2/sources`. A digest mismatch is a conflict; never retry it with a
guessed digest. Topology and Experiment updates likewise require a draft tied
to that exact stored source: one imported from it, or one that published it (or
was opened from the published diagram that did), with nothing else having
changed it since. Otherwise the update gets 409, for example `topology <name>
changed after this draft published it` or `experiment <name> changed after this
draft published it`. `POST /builder-v2/drafts` accepts
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

The Export dialog's Topology YAML saves `<diagram name>.topology.yaml`, the
Topology config Publish would write, from `POST /builder-v2/export/topology`
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
other reason; the export names interfaces without a VLAN only when a `vlan`
is missing or null, and hostnames only when one is a single character, which
the schema refuses too, and otherwise gives the schema's reason.

The Export dialog's Gephi (GEXF) saves `<diagram name>.gexf`, a GEXF 1.3
graph for Gephi that Builder v2 cannot open, made in the browser. Devices
and networks are its nodes, each connection an edge from a device to its
network, and their settings are columns; notes and groups are not nodes. It
lists each device's scenario apps (`apps`, `disabled_apps`), reading a stored
scenario as the Inspector does; when it cannot, it leaves the apps out and
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
Configs page, `phenix config delete`, `all` included, or the Builder v2 route
below), removes its published documents too, with or without the `builder-v2`
feature: a Topology config hook, which `api/builder` registers in every phenix
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

`GET /builder-v2/documents` lists only current documents: it lists each kind
of config once per request and matches the documents against their references
(`DocumentReference.Names`), rather than reading one config per document.
Stored rows carry `source: "store"`. A topology the caller may list whose
reference has a `path` and no current stored document gets a file row,
exactly `{source: "file", target, kind: "Topology", config, path}`: no `id`,
`digest`, `size`, `createdAt` or `createdBy`. The listing reads no file, so a
file row is listed even when the file is missing or invalid.

`DELETE /builder-v2/documents/{document}` deletes a published topology: the
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
the drafts page, each Published Diagrams card of a topology has Delete, for a
role with `configs` `delete`. It asks first ("Delete topology <name>?"), is
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
`Config.StoredJSON`). A string value of another shape, such as the 10-field
reference earlier builds of this branch wrote, is refused (`POST`/`PUT
/configs` answer 400); there is no migration.

```yaml
metadata:
  annotations:
    builder-doc:
      digest: sha256:5bbc6d046a1b98011f227ded600b90947bd1f44be35858654b4cae6b4cca9184
      id: b856fc9e35107594f72e715f74ee1cae3eb951b41bf09a504327924e0a220d34
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

Resolution (`topologyDocument` in `web/builder_v2_documents.go`): the stored
document the reference names for this topology wins; else, with a `path`,
the file, whose digest must equal `digest` when the reference has one (a
pin); else no document (`?topology=<name>` then says "No published Builder
v2 document exists for topology <name>."). A store error or corrupt record
is an error and does not fall through to the file.

Publish and `phenix builder publish` write `digest` and `id` and keep an
existing `path`. Nothing writes a Builder file.

The Topology config hook (`api/builder/config_hook.go`, registered in every
phenix process, with or without the feature, `--skip-validation` included)
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

The Configs page tags any topology with a `builder-doc` key `builder v2` and
sends its edit button to `/builder-v2?topology=<name>`, whatever the
reference names.

## Builder files

The one exception to "Builder v2 never reads server files": a Builder
document file named by `builder-doc.path`, read by
`bapi.ReadDocumentFile(root, excluded, path)` only when a diagram is
opened (`GET /builder-v2/topologies/{topology}/document`), when a draft is
created from it, and when such a draft updates the topology. The listing,
the hooks and the CLI never read it. Nothing is cached.

Rules (all checked at read time, by `phenix ui`):

- below `--base-dir.phenix` (default `/phenix`) and not below the VM mount
  directory (`common.MountDir()`, default `<base>/mounts`), also after
  resolving symbolic links; opened through `os.OpenRoot`, so a link that
  leaves the root is refused;
- a regular file (opened `O_NONBLOCK`; a FIFO or directory is refused), at
  most 5 MiB;
- one valid Builder document, JSON or YAML decided by content, not by the
  extension (which the path rule still requires). YAML goes through
  `builder.JSONFromYAML` (`types/builder/yaml.go`), which refuses anchors,
  aliases, merge keys, a second document, non-scalar or duplicate keys,
  other tags, `.inf` and `.nan`, and types scalars as js-yaml's
  `JSON_SCHEMA` does. No `${NAME}` expansion in the file;
- a `digest` beside `path` must equal the file document's digest.

Authorization is `configs` `get` on the topology. A forbidden or missing
topology, no or an invalid annotation, and a reference that names nothing
all answer the same 404 (`builder document of topology <name> not found`).
File errors are a closed set (`bapi.DocumentFileError`), each a fixed
sentence naming the path and never anything the file holds; the cause is
not wrapped, and the log line is `builder document file not usable` with
`topology`, `path`, `reason`:

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
YAML export as the config's `spec`).

Edit as a draft of a file diagram: `POST /builder-v2/drafts` with
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
so this draft cannot update it`. The client cannot tell beforehand, so the
dialog offers Update and shows the refusal. A publish writes `digest` and
`id`, keeps `path`, never writes the file, and adds to `warnings`:
`Topology <name> names the Builder file <path>, which Publish does not
change. Export the diagram and replace the file to keep it in step.` From
then on the stored document wins and the listing row is `source: "store"`.
On a server without that record, the file is used only when it matches the
`digest`.

In the UI a file row has the local id `file/<topology>`, a text tag File,
"Read from <path>", and no Delete; the read-only banner says "You are
viewing the diagram of topology <t>, read from <path> on the phenix
server.", plus a sentence when `topologyDiffers`.

## Document provenance

A document has four optional top-level string fields after `description`:
`author`, `createdAt`, `updatedBy`, `updatedAt`. Users are at most 256
bytes with no control characters; times are exactly
`YYYY-MM-DDTHH:MM:SSZ` (UTC, whole seconds); an empty string or null is
none; no pairing or ordering rule. Revision stays 1. They are document
content and part of its digest. `source.updatedAt` is a different field
(the imported config's time).

The server stamps them in `api/builder` when it stores a draft snapshot;
the editor never sets them:

- `POST /builder-v2/drafts` (`CreateDraft`): `author` and `createdAt` are the
  body's when present, else the caller and now; `updatedBy` and `updatedAt`
  are always the caller and now. The draft record keeps the two as
  `documentAuthor` and `documentCreatedAt`. So an Upload keeps the file's
  author, and anyone with `configs` `create` can name any author;
  `updatedBy` cannot be forged.
- Unchanged copy: with `sourceToken` `builder-doc/<id>` or
  `builder-file/<topology>/<digest>` and no `forkOf`, a body whose canonical
  JSON is the opened document is stored unstamped, so the draft's `digest`
  equals the document's and an unchanged publish answers topology
  `skipped`. A client must send the document exactly as `GET` returned it.
- `POST .../snapshots` (`AppendSnapshot`): `author` and `createdAt` come from
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
{author?, createdAt?, updatedBy?, updatedAt?}` (empty fields left out, `{}`
for an unchanged copy of a document that names nobody); the editor copies
it into its document (`withStamp` in `model.js`). No other response has
`stamp`.

`sourceFile` on `POST /builder-v2/drafts` records the name of the uploaded
file a draft came from (the UI sends it for Upload of a file and Import of
an uploaded config): a base name of at most 255 bytes, no `/` or `\`, not
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
`api/builder/publish.go`, command in `cmd/builder.go`), with the feature on
or off and with or without a running `phenix ui`. The CLI never says
"builder v2".

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
  now`. A `builder-xml` topology is never replaced. There is no force flag.
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

## Routes

All routes are relative to `/api/v1`.

| Route | Purpose |
|---|---|
| `GET /schemas/builder-v2/v1` | JSON Schema of the Builder document (`builder/v1`); needs `schemas` `get` on the resource name `builder-v2` |
| `GET/POST /builder-v2/drafts` | List the caller's drafts (`drafts`), other users' drafts the caller may see (`shared`) and unreadable drafts (`damaged`); create a draft (optionally `forkOf` or `sourceToken`) |
| `GET/DELETE /builder-v2/drafts/{owner}/{draft}` | Read a draft with its current document; delete it |
| `GET/POST /builder-v2/drafts/{owner}/{draft}/snapshots` | List or append snapshots (append needs `If-Match`) |
| `GET /builder-v2/drafts/{owner}/{draft}/snapshots/{snapshot\|current}` | Read one snapshot's document |
| `DELETE /builder-v2/drafts/{owner}/{draft}/snapshots/{snapshot}` | Delete a version other than the current one (needs `If-Match`) |
| `PATCH/PUT /builder-v2/drafts/{owner}/{draft}/cursor` | Undo and redo: move the draft's current snapshot |
| `POST /builder-v2/drafts/{owner}/{draft}/publish` | Create or update the topology, scenario and experiment configs |
| `GET/PUT /builder-v2/drafts/{owner}/{draft}/shares` | Read or replace who a draft is shared with (owner only) |
| `GET /builder-v2/drafts/{owner}/{draft}/shares/candidates` | Every account that can receive a share of the draft |
| `GET /builder-v2/sources` | Configs a document can be generated from or publish to |
| `POST /builder-v2/generate` | Build a document from a stored or uploaded Topology or Experiment |
| `POST /builder-v2/export/topology` | The Topology config a document publishes as, as YAML, with `warnings` and `publishBlockers` (nothing is written; needs `configs` `get`) |
| `GET /builder-v2/documents[/{document}]` | Published Builder documents (`source: "store"`); the listing also has a row per topology read from a Builder file (`source: "file"`) |
| `DELETE /builder-v2/documents/{document}` | Delete the topology a published document is current for, and the topology's published documents |
| `GET /builder-v2/topologies/{topology}/document` | The document a topology's `builder-doc` names, stored or read from its Builder file: the listing row plus `digest`, `size`, `document`, and for a file `topologyDiffers` |

Every route is behind the `builder-v2` feature: with it off, each answers a JSON `404`. The OpenAPI document served at `/docs/` describes every request and response.

## Storage

Drafts live in the phenix store as records, apart from configs. With an etcd
store, phenix compacts etcd's history for the whole cluster (see
`compaction-retention` in [cli.md](./cli.md)). When etcd reaches its space quota,
any write it refuses, including config writes (`POST`/`PUT /configs`) and
Builder v2 saves, answers `507` with `etcd is out of space: ...` as `message`
and an empty `cause`; the editor keeps the changes queued in the browser and
retries. Publish reports it in its `partial` result as `<stage> publication
failed: etcd is out of space: ...`. The error is logged once per request
(`store.ErrNoSpace`; the OpenAPI response is `InsufficientStorage`).

## Working on Builder v2 code

Read this section before changing any file listed below.

### Where the code is

| Area | Files |
|---|---|
| Document model, generation, publishing to configs, validation, JSON Schema, YAML reading | `src/go/types/builder/` (`document.go`, `generate.go`, `topology.go`, `validate.go`, `schema.go`, `yaml.go`) |
| Drafts, snapshots, sharing, published documents, limits | `src/go/api/builder/` (`service.go`, `shares.go`, `published.go`, `chunks.go`, `limits.go`, `validate.go`; `config_hook.go` checks a topology's `builder-doc` and removes a deleted or renamed topology's documents; `file.go` reads Builder files; `publish.go` publishes a document as a topology for the CLI) |
| `builder-doc` codec (nested in JSON and YAML, a string in memory and in the store) | `src/go/store/types.go` |
| `phenix builder publish`, and `phenix config create` recognizing Builder documents | `src/go/cmd/builder.go`, `src/go/cmd/config.go` |
| Record store for drafts (BoltDB and etcd, etcd compaction) | `src/go/store/*record*.go`, `src/go/store/etcd_record_compact.go` |
| HTTP routes, authorization and RBAC | `src/go/web/builder_v2*.go` (`builder_v2.go` holds the authorization model, `builder_v2_assets.go` serves the editor's files, which `src/js/plugins/builder-v2-assets.js` compresses in the UI build) |
| Editor page and drafts landing | `src/js/src/views/BuilderV2.vue`, `src/js/src/components/builder/BuilderDrafts.vue`, `BuilderHeaderButtons.vue` (the buttons both headers share) |
| Editor components | `src/js/src/components/builder/` (canvas, Inspector, outline, toolbar, side columns, `BuilderSignIn.vue`, dialogs, nodes, edges) |
| Editor state and logic | `src/js/src/builder/` (`store.js`, `model.js`, `autosave.js`, `idb.js`, `tabs.js`, `session.js`, `signin.js`, `panes.js`, `commands.js`, `keymap.js`, `layouts/`, `adapters/`) |
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
- The server serves the files only Builder v2 loads compressed, with their
  `Content-Length`, `Cache-Control: public, max-age=31536000, immutable` and
  `Vary: Accept-Encoding`: Brotli or gzip, as the request's `Accept-Encoding`
  weighs them (Brotli on a tie), else uncompressed. The UI build compresses
  them: the plugin in `src/js/plugins/builder-v2-assets.js` starts from the
  `src/views/BuilderV2.vue` chunk, leaves out files the rest of the UI also
  loads, writes each file's `.br` and `.gz` copies next to it when they are
  smaller, and lists the files in `builder-v2-assets.json` beside
  `index.html`. The server sends nothing else compressed and answers a
  request for a copy itself with 404. Renaming or moving `BuilderV2.vue`
  means updating the `view` passed to the plugin in `src/js/vite.config.js`;
  the build fails when it finds no chunk for it. Without the list, every file
  is served uncompressed and the server logs a warning at startup.
- The rest of the UI loads `src/builder/api.js` to send saves left unsent, so
  what it imports from `src/builder/` lands in a chunk the server sends
  uncompressed. Keep the decoder and validator out of it: the document size
  limit it shares with `decode.js` lives in `limits.js`, which imports
  nothing. A test in `api.test.js` checks this.
- Keep this file current when Builder v2 behavior changes.

### Tests

- Go: `go test ./api/builder ./types/builder ./store ./web` from `src/go`.
  The `api/builder` and `web` tests share one in-memory record store,
  `src/go/store/recordtest/memrecord`, with the hooks `BeforeCreate`,
  `BeforeUpdate`, `AfterWrite`, `FailDelete`, `FailPrefixDelete` and `Stamp`.
  Tests that edit stored records use its methods (`Count`, `Keys`,
  `SetValue`, `Drop`, `RewriteLocked`), not its fields. It is a package of its
  own because the `store` tests import `recordtest`.
- Unit: `npx vitest run test/builder` from `src/js`.
- Browser: the `builder*.spec.js` Playwright specs in `src/js/e2e/tests/` need a
  server started with `--features builder-v2`. `builder-feature-off.spec.js`
  needs a second server without the feature and `E2E_BUILDER_V2=off`.
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
