# Saving and storage

Part of the [Builder references](../builder.md). Snapshots, the browser's
queue of unsaved changes, the merge on a conflict, and the records drafts
live in.

User docs: [How drafts save](https://phenix.sceptre.dev/latest/builder/drafts/#how-drafts-save),
[Draft History](https://phenix.sceptre.dev/latest/builder/drafts/#draft-history)
(with the snapshot limits) and
[Storage](https://phenix.sceptre.dev/latest/builder/administration/#storage).

## Snapshots

- A save appends a snapshot: `POST /builder/drafts/{owner}/{draft}/snapshots`
  with `If-Match`. It may carry `opId`, the client's id for the save (1-128
  letters, digits, `.`, `-`, `_`, starting with a letter or digit). The
  manifest keeps it and listings return it, so a client whose answer was
  lost can tell that the server stored the save.
- `GET …/snapshots` returns `{"cursor": <index>, "snapshots": [...]}`,
  oldest first. Each has `id`, `digest`, `size`, `createdAt`, `createdBy`,
  `current`, and `summary` and `opId` when set.
- `PATCH`/`PUT …/cursor` moves the current snapshot (undo, redo, restore).
- `DELETE …/snapshots/{snapshot}` with `If-Match` removes a version. Those
  who may save may delete. It answers 200 with the draft and its new
  `ETag`, and 409 `draft.snapshot.current` for the cursor's snapshot. The
  cursor stays on the same snapshot.
- Snapshots never share chunks, so a delete takes no content from another
  version or a published document.
- When a dropped or deleted snapshot is the one the last publication
  names, the server keeps that publication. The draft is then dirty, and it
  (or a draft that forks it) can still update what it published.

## The browser queue

`builder/autosave.js` keeps unsaved edits in IndexedDB `phenix-builder`
until the server confirms them. Each tab keeps its own queue of a draft
(`tabs.js`). Tabs find each other by Web Locks or, without them, by the
tabs that answer within 500 ms over the BroadcastChannel
`phenix-builder:tabs` (localStorage events without one). Logout leaves the
changes of other open tabs to those tabs.

## Merge on a conflict

A save refused with 412 reads the draft again.

1. When the draft as read already holds the save (for a snapshot, the
   current one carries the save's `opId`), the server stored an earlier
   delivery and its answer was lost. The read is the save's answer
   (`alreadyStored`). Nothing is merged or sent again.
2. Else, when its head (current snapshot, cursor, count) is the one the
   queue last confirmed, the queue takes the new ETag and sends again.
3. Else `store.mergeConflict` merges three ways (`mergeDocuments(base,
   mine, theirs)` in `merge.js`). The base is the snapshot of the queue's
   `serverHead`, from the undo history or the queue, else read with
   `GET …/snapshots/{snapshot}` (`MERGE_BASE_TIMEOUT_MS`, 15 s).

Merge rules that code depends on:

- Nodes, networks, edges and templates match by id, icons by name, other
  lists by a unique `id`, else `name`. A device's spec interfaces match by
  the id of the handle of the same name (`tagSpecInterfaces`), so a rename
  on one side and another change on the other side is one interface.
- `viewport` is mine. `$schema`, `revision`, `metadata.id` and the four
  stamps are theirs. Order is theirs, then mine's additions.
- A device's `label`, `hostname` and `spec.general.hostname` are one choice
  (`settleDeviceName`). An interface both sides renamed is one clash
  (`settleInterfaceNames`). A node or interface that one side deleted and
  the other connected is a clash too (`connectionTarget`).
- `dropDangling` then removes each edge whose end the merged document
  lacks.
- Without clashes, `saveMerged` replaces the queue with one snapshot and
  sends it with the server copy's ETag (`rebase` in `autosave.js`). With
  clashes, `dialogs/MergeDialog.vue` asks. If the rebase throws,
  `saveMerged` restores what it replaced and rethrows. Edits are refused
  while a merge runs (`refuseWhileResolving`).

User docs: [When the draft changed on the server](https://phenix.sceptre.dev/latest/builder/drafts/#when-the-draft-changed-on-the-server).

## Storage

Drafts and the libraries are records in the phenix store, apart from
configs, so `phenix config list` does not list them.

| Bucket | Keys |
|---|---|
| `builder.drafts`, `builder.chunks`, `builder.published` | Drafts, snapshot content, published documents. The startup cleanup lists only these three. |
| `builder.templates` | `lib/<OwnerScope(user)>`, and the hints `in/…` and `pub/…` (see [templates-and-icons.md](templates-and-icons.md#template-library)) |
| `builder.icons` | `name/<lowercase icon name>` (see [templates-and-icons.md](templates-and-icons.md#icon-library)) |

`OwnerScope(user)` is the lowercase hex SHA-256 of the user name
(`api/builder/scope.go`). A library follows the user name, as draft
ownership does. Nothing removes a library or an icon when an account is
deleted. With etcd, phenix compacts history for the whole cluster (see
`compaction-retention` in [cli.md](../cli.md)). For the 507 answer when
etcd is full, see [api.md](api.md#etags).
