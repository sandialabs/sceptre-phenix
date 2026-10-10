# Saving and storage

Part of the [Builder references](../builder.md). Snapshot appends, the
browser's queue of unsaved changes and its tabs, the three-way merge on a
conflict, Draft History, and the records drafts live in.

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

### Merge on conflict

A save refused with `412` reads the draft again; when its head (current
snapshot, cursor, snapshot count) is the one the queue last confirmed, the
queue takes the new ETag and sends again, else the conflict carries the draft
as read (`serverCopyOf` in `autosave.js`) to `store.mergeConflict`, as does a
recovered queue based on an older ETag. The store merges three ways
(`mergeDocuments(base, mine, theirs)` in `merge.js`): the base is the snapshot
of the queue's `serverHead`, from the undo history or the queue's entries, else
`GET .../snapshots/{snapshot}` (given `MERGE_BASE_TIMEOUT_MS`, 15 s, while the
panel stays hidden; meanwhile `store.mergingConflict` is true, the save
state reads `Merging your changes with the server version…`
(`MERGING_TEXT`, class `builder-status--merging`, no warning sign), and the
live region says it once per merge, not again when a newer version restarts
it); mine is the diagram on screen; theirs the server copy. Nodes, networks, edges and templates match by id, icons by name, other
lists by a unique `id` or else `name` (device interface handles by `id`)
and otherwise are one value, as are a node's `position` and `size`; a
device's spec interfaces match by the id of the handle of the same name
(a `HANDLE` symbol `tagSpecInterfaces` adds and `untagSpecInterfaces`
removes; `name:<name>` where no handle has the name), so a rename on one
side and another change on the other is one interface;
objects merge key by key; `scenarios` merge as a set; `viewport` is mine;
`$schema`, `revision`, `metadata.id` and the four stamps are theirs; order is
theirs, then mine's additions. A device's `label`, `hostname` and
`spec.general.hostname` are one choice: when any of them clashes, one clash
covers all three and the chosen side's values are kept for all three
(`settleDeviceName`); a switch's label never clashes. An interface both
sides renamed differently is one clash, `<device> interface <old> name`,
keyed by its handle (`settleInterfaceNames`), and the chosen name goes on
the handle and its spec entry in every version. A node, or a device's
interface (handle or spec entry), that one side deleted and the other
connected (a new or changed edge ends at it, `connectionsOf`) is a clash
too, at the node or the handle (`connectionTarget`; the interface lists are
then merged item by item, `forceItems`), with "delete <node> and drop your
connection to it" and "keep <node> with your connection" (or "with your
changes" when that side changed it as well). After the merge,
`dropDangling` removes each edge whose node or device handle the merged
document lacks and lists it in `dropped`; the announcement then adds
`droppedText` ("Dropped the connection from <a> to <b>: the device or
interface it connects was deleted."). Without
clashes, and when `parseDocument` takes the result, `saveMerged` replaces the
queue with one snapshot `Merged changes from <user>` sent with the server
copy's ETag (`rebase` in `autosave.js`), the undo history becomes the server
copy then the merged document, and the store announces `Merged <user>'s
changes with yours.` (`another tab` when the server names the user,
`another editor` without sign-in; `changesFrom`). Otherwise `store.merge` is
`review` (the panel's Review and merge, `conflict-merge`, opens
`dialogs/MergeDialog.vue`: one fieldset per clash with Keep mine / Keep
theirs, Keep all mine, Keep all theirs, Save merged aria-disabled until each
has a choice, and always with no clash, when the dialog opens only for
what `parseDocument` refuses: it lists that and says Cancel offers saving
the history as a new draft or discarding it; `store.saveMergeChoices`, which answers
`{saved: false, busy: true}` while another merge runs and the dialog then says
"Another change arrived; the merge is being redone.", and `{saved: false,
error}` when `autosave.rebase` throws: `saveMerged` then puts back the
diagram, history, selection and ETag it replaced and rethrows, the review
stays, and the dialog shows `The merged diagram could not be saved:
<message>. Try again, or cancel …` (`mergeSaveFailure`) with the choices
kept) or `unavailable` (no base, the base read
timed out, or the merge threw: `Merging failed: <message>`; one more sentence,
`conflict-merge-note`). Fork and discard stay. Edits are refused
while a merge runs (`refuseWhileResolving`). A conflict on the merged save
merges again with the new server copy, from the head the merge was based on.

### Snapshots and Draft History

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

### Partial cleanup failures

A mutation whose durable write succeeded but whose superseded content could not
be removed returns its normal success status, body, and new `ETag`, plus a
`Warning: 199` header naming the operation; the cause is logged, never sent.
Failing such a request would only make the client retry with a stale tag.

## Storage

Drafts live in the phenix store as records, apart from configs
(`builder.drafts`, `builder.chunks`, `builder.published`; the startup cleanup
lists only these three). The template libraries are records in
`builder.templates`, keyed through `OwnerScope(user)` (the lowercase hex
SHA-256 of the user name, `api/builder/scope.go`): a library follows the
user name, as draft ownership does, and nothing removes it when an account
is deleted. The icon library is records in `builder.icons` keyed by
`name/<lowercase icon name>`; an icon's `owner` is the uploader's user name,
and nothing removes an icon when its uploader's account is deleted. With an
etcd
store, phenix compacts etcd's history for the whole cluster (see
`compaction-retention` in [cli.md](../cli.md)). When etcd reaches its space quota,
any write it refuses, including config writes (`POST`/`PUT /configs`) and
Builder saves, answers `507` with `etcd is out of space: ...` as `message`
and an empty `cause`; the editor keeps the changes queued in the browser and
retries. Publish reports it in its `partial` result as `<stage> publication
failed: etcd is out of space: ...`. The error is logged once per request
(`store.ErrNoSpace`; the OpenAPI response is `InsufficientStorage`).
