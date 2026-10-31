# Saving and storage

Part of the [Builder references](../builder.md). User docs:
[How drafts save](https://phenix.sceptre.dev/latest/builder/drafts/#how-drafts-save),
[Draft History](https://phenix.sceptre.dev/latest/builder/drafts/#draft-history) and
[Storage](https://phenix.sceptre.dev/latest/builder/administration/#storage).

## Snapshots

- `POST /builder/drafts/{draft}/snapshots` with `If-Match` appends a
  snapshot. The optional `opId` (1-128 letters, digits, `.`, `-`, `_`,
  letter or digit first) is the client's save id. Listings return it, so a
  client that lost the answer can find the save.
- `GET …/snapshots` returns `{"cursor": <index>, "snapshots": [...]}`,
  oldest first. Each has `id`, `digest`, `size`, `createdAt`, `createdBy`,
  `current`, and `summary` and `opId` when set.
- `PATCH …/cursor` moves the current snapshot (undo, redo, restore). Undo
  and redo show the entry and queue the move. `restoreSnapshot` changes the
  editor only after its move ends `saved`. Else `withdrawCursor`
  (`autosave.js`) drops the move, and the page alert tells why.
- A save after the cursor deletes the redo branch, also the snapshots of
  other users (`AppendSnapshot`, `api/builder/service.go`). The editor
  warns first:
  - `store.draftHead` is `headOf` the last draft answer (`rememberDraft`).
    The `newerHistory` getter (`newerSnapshots`, `newerHistory.js`) counts
    `snapshots - 1 - cursor` only while the current undo entry's
    `serverSnapshotId` is `draftHead.snapshotId`. So an unsaved change or
    a cursor move the server did not accept yet shows nothing.
  - `onDraft` updates `serverHistory` locally (`historyAfter`: a save
    replaces the redo branch, the oldest snapshots go, `current` follows
    the head). So the notice names the newest snapshot's user and time
    without a read. On a mismatch (`newest: null`), `BuilderNewerHistory.vue` calls
    `readNewerHistory` once per head. It drops a list that arrives after a
    head change or during a History dialog read.
  - The editor announces the notice once per draft while the head has
    newer snapshots, so an undo does not announce it again.
  - `returnToLatest` moves to the newest snapshot's undo entry (one cursor
    move), else calls `restoreSnapshot(id, {label})`. `saveNewerAsDraft`
    creates a draft of `duplicateCopy` from the newest snapshot without
    opening it, and sets `newerSaved`. History's Restore states the count
    (`restoreNote`, tooltip, `history-restore-note-<index>`).
  - Tests: `newer-history.contract.test.js`, `newer-history-store.test.js`,
    `newer-history-render.test.js`.
- `DELETE …/snapshots/{snapshot}` with `If-Match` deletes a version. Any
  user who may save may delete. It answers 200 with the draft and its new
  `ETag`, or 409 `draft.snapshot.current` for the cursor's snapshot. The
  cursor stays on its snapshot. Each snapshot has its own content record,
  so a delete never deletes content that another version or a published
  document uses.
- Each save, cursor move, snapshot delete and draft delete is one
  `store.RecordStore.RecordTxn`. Content records, the metadata
  compare-and-swap and the deletion of discarded content commit together
  or not at all. A refused save stores nothing. No warning header follows
  a draft change.
- When the last publication names a dropped or deleted snapshot, the
  server keeps the publication. The draft is dirty, and it or a fork of it
  can still update the publication.

## The browser queue

`builder/autosave.js` keeps unsaved edits in IndexedDB `phenix-builder`
until the server accepts them, one queue per tab and draft (`tabs.js`).
Tabs find each other by Web Locks. Else they use the tabs that answer
within 500 ms on BroadcastChannel `phenix-builder:<user>::<draft id>`, else
localStorage events.

- Requests: no debounce or coalescing, because each edit is one snapshot
  and one undo step. No presence, heartbeat or polling. The changes of
  other editors arrive only through a 412. The queue sends three requests
  only: the read after a conflict, the snapshot append and the cursor
  move. It sends the last two in order, awaited, with `If-Match`. A read
  that finds the draft deleted stops the queue as deleted.
- Tabs: a duplicated tab copies sessionStorage, finds the Web Lock of its
  id taken, and takes a new id (`claimTab`). A queue whose tab holds no
  lock belongs to a closed tab. Other tabs request that lock, so they learn
  when a page closes, crashes or is discarded.
- Records (`idb.js`): one record per tab and draft (`tabRecordKey`) with
  the queue, its ETag and the metadata of the history entries it needs.
  Each entry's snapshot is a separate record, written once.
- Page close can stop an IndexedDB write. So `keepForUnload` at once
  copies the operations queued since the last finished write, with their
  snapshots, to localStorage (`writeUnloadCopy`, `phenix.builder.` keys by
  user, draft and tab). The next draft read merges the copy
  (`mergeUnloadCopy`), unless the record is newer (`staleUnloadCopy`). A
  copy that does not fit is skipped. The caller then keeps the browser's
  leave question until the store holds the edits. Logout clears the copies.
- Refused operations: the server stores no snapshot it refused as invalid
  or too large, and no cursor move to a pruned snapshot. When a later
  operation is queued, `dropRejected` drops the refused operation and the
  cursor moves that point at it. The local undo history keeps the entry.
- Background saves (`leave.js`): the Drafts button gives the queue to
  `createBackgroundSaves`, which keeps sending while the card shows
  `backgroundSaveCard`. After a local storage failure
  (`saveState.storageFailed`), `mayClose` of `createLeaveGuard` waits for
  the queue as `mayLeave` does, because the queue goes with the page. A
  draft that opens again or is deleted
  first calls `release(id)`. Background saves end after the current send,
  and the draft's own queue sends the rest.

Every `builderApi` request has a time limit (`withTimeouts` in `api.js`,
`REQUEST_TIMEOUTS`): 30 s for GET, 60 s for other methods, 10 min for
`ROUTE_TIMEOUTS` (publish and its dry run, preflight, generate, legacy,
export, package, resolve, cleanup). At the limit, the request aborts with
`code: 'ETIMEDOUT'` and no response. `classifyError` gives `offline`, and
the queue retries on `RETRY_DELAYS` with **Retry saving** shown. A stored
append that timed out meets 412 on the retry, and `alreadyStored` finds its
`opId`. When the read after a 412 gets no answer (`unanswered`),
`readAgain` throws that error, not the 412, so the queue retries. The
shared axios instance (`utils/axios.js`) has no time limit.

## Merge on a conflict

After a 412, the queue reads the draft again:

1. If the draft holds the save (the current snapshot carries its `opId`),
   the server stored an earlier delivery. The read is the answer
   (`alreadyStored`), and the queue merges and sends nothing.
2. Else, if the head (current snapshot, cursor, count) is the last head
   from the server that the queue keeps (`serverHead`), the queue takes the
   new ETag and sends again.
3. Else `store.mergeConflict` runs `mergeDocuments(base, mine, theirs)`
   (`merge.js`). The base is the snapshot of the queue's `serverHead`, from
   the undo history or the queue, else from `GET …/snapshots/{snapshot}`
   (`MERGE_BASE_TIMEOUT_MS`, 15 s).

Merge rules that code depends on:

- Nodes, networks, edges and templates match by id, icons by name, other
  lists by a unique `id`, else `name`. Spec interfaces match by the id of
  the handle with the same name (`tagSpecInterfaces`). So a rename on one
  side and a change on the other apply to one interface.
- `viewport` is mine. `$schema`, `revision`, `metadata.id` and the four
  stamps are theirs. Order is theirs, then the additions of mine.
- A template's `source` is one value ("library link",
  `ATOMIC_TEMPLATE_KEYS`). An absent or empty `layers` merges as the
  placeholder layers.
- A device's `label`, `hostname` and `spec.general.hostname` are one
  clash (`settleDeviceName`). An interface that both sides renamed is one
  clash (`settleInterfaceNames`). A node or interface that one side
  deleted and the other connected is one clash (`connectionTarget`).
- A group that one side deleted or ungrouped while the other put nodes in
  it (`membersOf`: added with that `parentId`, or moved to it) is a clash
  on the group (`filled`). A `parentId` that one side set to a group that
  the other put inside the node is a clash on that field (`nests`,
  `settleParent`). Both rules are symmetric: swapped sides give the same
  clash keys. Choices show a `parentId` as the group's name or "no group".
- `settleParents` then moves each node whose `parentId` is missing or is
  not a group to the nearest group that held that group (theirs, mine,
  base), as `ungroup` does. It then moves one node of each remaining cycle
  out of its group (one that changed group, the first by id). Moves go in
  `moved` (`{key, label, reason}`, `movedText`). `merge.contract.test.js`
  checks that no merged document, for any choices, has a `node.parent.*`
  issue.
- `dropDangling` then drops each edge whose end is missing.
- Without clashes, `saveMerged` replaces the queue with one snapshot, sends
  it with the server's ETag (`rebase`, `autosave.js`), and announces
  `mergeNotes(result)` (dropped connections, then moved nodes). With
  clashes, `dialogs/MergeDialog.vue` asks. If the rebase throws,
  `saveMerged` puts back the queue and rethrows. Edits are refused during
  a merge (`refuseWhileResolving`).
- After the save, the undo history holds the server's version, then the
  merged document, one step apart. The entries of the unsaved edits are
  dropped, not rebased. Each holds the diagram before the other editor's
  changes, so an undo to one would lose those changes. The server holds
  none of them, so a cursor move to one could not save. The merged
  document holds all of those edits.
- Discard (`settleConflict('reload')`) reads the draft first. It calls
  `discardLocal` and `loadDraft(id, {envelope})` only after a successful
  read, so a failed read keeps the queue.
- Save as a new draft (`forkLocalHistory`) creates the draft, appends each
  entry and moves the cursor. If a later request fails, it deletes the new
  draft (best effort) and rethrows.

User docs: [When the draft changed on the server](https://phenix.sceptre.dev/latest/builder/drafts/#when-the-draft-changed-on-the-server).

## Storage

Drafts and libraries are phenix store records, not configs, so
`phenix config list` does not list them.

| Bucket | Keys |
|---|---|
| `builder.drafts`, `builder.content`, `builder.published` | Draft metadata (one record per draft ID), content and published-document metadata. Content is one gzip record each (`drafts/<draft>/<snapshot>`, `published/<document>`), checked by size and sha256 digest on read. The orphan sweep lists only these three. |
| `builder.templates` | `library`, the server's one template library (see [templates-and-icons.md](templates-and-icons.md#template-library)) |
| `builder.layersets` | `set/<id>` per layer set, and `default` for the server's default set (see [templates-and-icons.md](templates-and-icons.md#layer-set-library)) |
| `builder.icons` | `name/<lowercase icon name>` (see [templates-and-icons.md](templates-and-icons.md#icon-library)) |

An account delete deletes no default layer set, library item or icon.
BoltDB stores each record as a 25-byte header (format byte, revision,
created and updated times), then the raw value (`store/bolt_record.go`).

Limits and cleanup:

- `MaxDrafts` (2000, `api/builder/limits.go`): `CreateDraft` counts the
  keys of `builder.drafts`, damaged drafts included. Past the limit it
  returns `DraftLimitError` (413, `draft.limit`). `GET /builder/drafts`
  reports `limits.drafts`, and `limits.js` has `MAX_DRAFTS`.
- The orphan sweep (`removeOrphans`, `web/builder_cleanup.go`) runs at
  start and every `OrphanGracePeriod` (1 h) until `web.Start` returns. It
  deletes published documents that no topology references
  (`CleanupOrphanedDocuments`) and content records that no metadata names
  (`CleanupOrphanedContent`), when older than the grace period by
  `Updated` time.
- `POST /builder/cleanup` (`builder` `use` only) runs the sweep. It also
  deletes each draft that is `DraftMetadata.PublishedUnchanged` (clean, no
  redo snapshot) with a live `Publication` (its topology's `builder-doc`
  names its `DocumentID`), at the listed revision (`DeletePublishedDrafts`).
  It holds the publish lock while it lists and deletes (see
  [publishing.md](publishing.md#finishing-a-partial-publication)).
  `?dryRun=true` counts and deletes nothing. The answer is
  `{dryRun, drafts: [ids], documents, content}`. Two cases only add a
  `Warning`: a failure after the draft deletes, and a published record
  that does not decode (the sweeps skip it and keep its content).
- **Clean up…** (`BuilderSettings.vue`) runs the dry run, asks, then runs
  the cleanup (`store.planStorageCleanup`, `store.cleanUpStorage`). It
  first saves the open draft's queue, and stops unless that save ends
  `saved` with nothing pending. When the dry run names a draft of any user
  with a non-empty IndexedDB queue (`unsentDraftIds`, `store.js`), it
  returns `{unsent: n}` and deletes nothing. It sets the page alert when it
  deleted the open draft.
