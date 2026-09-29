# Builder Flow

Builder Flow is phenix's web topology editor, built on Vue Flow. It is a beta
behind the `builder-beta` UI feature. This file holds everything about it
that the main phenix skill leaves out.

**Read this file when** a task involves Builder Flow: its drafts, sharing,
publishing, import or generation, its `/api/v1/builder/*` or
`/schemas/builder/v1` routes, the `builder-drafts` RBAC resource, the Builder
document format (`builder/v1`), or any Builder Flow code (see
[Working on Builder Flow code](#working-on-builder-flow-code)). The legacy
Builder (`/builder`, `builder-xml` topologies) is a different editor and is
covered by the main skill.

## Enabling it

`phenix ui --features builder-beta` enables Builder Flow, the Vue Flow
topology editor (a beta), at `/builder-beta` and its draft/document APIs. It
leaves the legacy `/builder` route available for `builder-xml` topologies.
Builder Flow has no `phenix` CLI command: use the REST API or the web UI.
Drafts autosave separately from phenix configs; only the explicit Publish action
creates or updates topology, scenario, or experiment configs. Its Router and
Firewall device templates create `minirouter` nodes (image `minirouter.qc2`)
of type `Router` and `Firewall`, which the `vrouter` app configures. The
Inspector suggests drive images, and the diagram checks flag a missing one,
from `GET /disks`; without the `disks` `list` permission it does neither.

Features are disabled by default and require restarting `phenix ui` after
changing `ui.features`.

## In the browser

Builder Flow works over plain HTTP as well as HTTPS. A published diagram
opens read only; Edit as a draft creates a draft from it (or reopens the draft
made from it before), which needs `configs` `create`, so a role with only
`list`/`get` can view drafts and published diagrams but cannot create, import,
upload, or publish. Configs' edit button for a Builder Flow topology links to
`/builder-beta?topology=<name>`, which opens the user's draft of it (making one
the first time) and then names it as `?draft=<owner>/<id>`, so a reload reopens
that draft; with the feature off, Configs explains that the topology can only
be edited in Builder Flow. The Inspector also edits a node's labels,
annotations, and advanced (minimega `vm config`) settings. Leaving a draft,
publishing, or exporting first saves Inspector changes that were not applied,
as a draft snapshot with the summary `Saved unapplied changes to <node>`.
Logging out removes Builder Flow's local drafts (IndexedDB `phenix-builder`)
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
it also offers Sign in again. A draft keeps its own layout choice in its
document's `layout`; the Settings layout is the default for drafts without one.
A document may also hold each connection's `route` as a layout drew it;
publishing and export ignore both.

When the server refuses the session (a `401` on a save, a listing or a
publish), or the token expires while Builder Flow is open, a Sign in again
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
puts Blank diagram, Import and Upload before them; the editor puts the save
state, Warnings and Reset view before them and Shortcuts after the theme
button. Below a 105rem header (about a 1712px window) the theme button,
Shortcuts, Settings and Help show only their icons and Commands drops its key
caps; below 85rem (about 1392px) Commands and Reset view show only their icons
too. Warnings, Blank diagram, Import and Upload keep their labels. The
editor's toolbar has Draft History right after Minimap. Focus
mode (⇧⌘F or Ctrl+Shift+F) works on both views and stays on between them,
until the user turns it off or leaves Builder Flow.

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
(`name` is the first and last name, or `""`). It holds the users the caller may
view the way `GET /users` lists them, so it is empty without `users` `list`,
and leaves out the owner and anyone a `PUT` would refuse; users already shared
with are listed. It has the same access rules as `PUT .../shares`. The Share
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

Every Builder Flow request also needs the base `configs` permission of the verb
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

## Saving: snapshots, ETags and the local queue

A snapshot append (`POST /builder/drafts/{owner}/{draft}/snapshots`) may carry
`opId`, the client's id for the save (1-128 letters, digits, `.`, `-`, `_`,
starting with a letter or digit). The snapshot manifest keeps it, and snapshot
listings return it, so a client whose response was lost can tell the snapshot
was stored. A draft keeps at most 50 snapshots and 50 MiB of them; past either,
the oldest are dropped. The UI keeps unsaved Builder Flow edits in the browser's
`phenix-builder` IndexedDB database only until the server confirms them. Each
browser tab keeps its own queue of a draft (the tab's id is `phenix.builder.tab`
in sessionStorage). When more than one tab holds unsaved changes to one draft,
the user chooses which to save, and the others are saved as new drafts. Logout
leaves the changes of other open tabs to those tabs. It finds them by their
Web Locks or, without Web Locks, by the tabs that answer within 500 ms over the
BroadcastChannel `phenix-builder:tabs` (localStorage events without one).

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
number, name, date and user, with Restore and Delete in each row (clicking a
name also restores). The current row can be neither restored nor deleted, a
view-only user gets no actions, and undo and redo skip a deleted snapshot.
After a 412 the editor reads the draft again and the next try uses that ETag.

A mutation whose durable write succeeded but whose superseded content could not
be removed returns its normal success status, body, and new `ETag`, plus a
`Warning: 199` header naming the operation; the cause is logged, never sent.
Failing such a request would only make the client retry with a stale tag.

## Sources and generation

`GET /builder/sources` groups configs by kind: `topologies` and `experiments`
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

`POST /builder/generate` accepts either `{"source":"Topology/name"}` (or an
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
caller's `configs` `get` and `topologies` `list` permissions; a missing,
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
`digest` as `GET /builder/sources` lists them, never its content. Generating
from a stored Experiment whose `scenario` annotation names a Scenario the
caller may list produces such a reference, so the draft publishes back with
scenario action `use`. If that Scenario is missing or hidden from the caller,
or the Experiment was uploaded, the Experiment's embedded copy is attached as
an uploaded scenario of the same name, with a warning: an uploaded Experiment
is never bound to this server's Scenario of that name, which may differ.

Publishing an uploaded Scenario with action `update` requires
`scenario.expectedDigest`, copied from a fresh matching entry returned by
`GET /builder/sources`. A digest mismatch is a conflict; never retry it with a
guessed digest. Topology and Experiment updates likewise require a draft tied
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
that document was published to, otherwise 404. A published
topology names its document in its `builder-doc` annotation; a published
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

After a publication, older published documents of the same topology are
removed once they are more than an hour old; newer ones go at a later publish
or at the startup cleanup.

## Routes

All routes are relative to `/api/v1`.

| Route | Purpose |
|---|---|
| `GET /schemas/builder/v1` | JSON Schema of the Builder document (`builder/v1`) |
| `GET/POST /builder/drafts` | List the caller's drafts (`drafts`), other users' drafts the caller may see (`shared`) and unreadable drafts (`damaged`); create a draft (optionally `forkOf` or `sourceToken`) |
| `GET/DELETE /builder/drafts/{owner}/{draft}` | Read a draft with its current document; delete it |
| `GET/POST /builder/drafts/{owner}/{draft}/snapshots` | List or append snapshots (append needs `If-Match`) |
| `GET /builder/drafts/{owner}/{draft}/snapshots/{snapshot\|current}` | Read one snapshot's document |
| `DELETE /builder/drafts/{owner}/{draft}/snapshots/{snapshot}` | Delete a version other than the current one (needs `If-Match`) |
| `PATCH/PUT /builder/drafts/{owner}/{draft}/cursor` | Undo and redo: move the draft's current snapshot |
| `POST /builder/drafts/{owner}/{draft}/publish` | Create or update the topology, scenario and experiment configs |
| `GET/PUT /builder/drafts/{owner}/{draft}/shares` | Read or replace who a draft is shared with (owner only) |
| `GET /builder/drafts/{owner}/{draft}/shares/candidates` | Users the owner may share the draft with |
| `GET /builder/sources` | Configs a document can be generated from or publish to |
| `POST /builder/generate` | Build a document from a stored or uploaded Topology or Experiment |
| `GET /builder/documents[/{document}]` | Published Builder documents |

Every route is behind the `builder-beta` feature: with it off, each answers a JSON `404`. The OpenAPI document served at `/docs/` describes every request and response.

## Storage

Drafts live in the phenix store as records, apart from configs. With an etcd
store, phenix compacts etcd's history for the whole cluster (see
`compaction-retention` in [cli.md](./cli.md)). When etcd reaches its space quota,
any write it refuses, including config writes (`POST`/`PUT /configs`) and
Builder Flow saves, answers `507` with `etcd is out of space: ...` as `message`
and an empty `cause`; the editor keeps the changes queued in the browser and
retries. Publish reports it in its `partial` result as `<stage> publication
failed: etcd is out of space: ...`. The error is logged once per request
(`store.ErrNoSpace`; the OpenAPI response is `InsufficientStorage`).

## Working on Builder Flow code

Read this section before changing any file listed below.

### Where the code is

| Area | Files |
|---|---|
| Document model, generation, publishing to configs, validation, JSON Schema | `src/go/types/builder/` (`document.go`, `generate.go`, `topology.go`, `validate.go`, `schema.go`) |
| Drafts, snapshots, sharing, published documents, limits | `src/go/api/builder/` (`service.go`, `shares.go`, `published.go`, `chunks.go`, `limits.go`, `validate.go`) |
| Record store for drafts (BoltDB and etcd, etcd compaction) | `src/go/store/*record*.go`, `src/go/store/etcd_record_compact.go` |
| HTTP routes, authorization and RBAC | `src/go/web/builder_beta*.go` (`builder_beta.go` holds the authorization model) |
| Editor page and drafts landing | `src/js/src/views/BuilderBeta.vue`, `src/js/src/components/builder/BuilderDrafts.vue`, `BuilderHeaderButtons.vue` (the buttons both headers share) |
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
- Keep this file current when Builder Flow behavior changes.

### Tests

- Go: `go test ./api/builder ./types/builder ./store ./web` from `src/go`.
- Unit: `npx vitest run test/builder` from `src/js`.
- Browser: the `builder*.spec.js` Playwright specs in `src/js/e2e/tests/` need a
  server started with `--features builder-beta`. `builder-feature-off.spec.js`
  needs a second server without the feature and `E2E_BUILDER_BETA=off`.
  `builder-sharing.spec.js` (sharing, and signing in again) needs a server
  with sign-in on (a JWT signing key and an admin user) and `E2E_SHARING=1`.
  `src/js/e2e/README.md` has the setup, the `@known-defect`, `@cross-browser`
  and `@axe` tags, the Playwright projects, and how CI splits the suite into
  parallel jobs. Tag `@axe` only on full axe scans: CI runs them in a job of
  their own, apart from the smoke shards.
- CI runs the e2e suite on Linux, where key names differ from macOS (Enter,
  not Return), and on slower machines.
