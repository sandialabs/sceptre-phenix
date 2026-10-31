# Working on Builder code

Part of the [Builder references](../builder.md): code locations, build
rules and tests.

## Where the code is

[Architecture](../builder.md#architecture) and the
[routing table](../builder.md#routing-table) name the main directories and
files. Other code:

| Area | Files |
|---|---|
| The editor's compressed files | `src/go/web/builder_assets.go` |
| Builder role and its start-up check | `src/go/api/config/default/builder.yml`, `src/go/web/rbac/migrations.go`, `src/go/web/init.go` |
| `builder-doc` codec | `src/go/store/types.go` |
| Record store (BoltDB) | `src/go/store/*record*.go` |
| `phenix config create` and Builder files | `src/go/cmd/config.go` |
| Configs page links | `src/js/src/components/configs/ConfigsList.vue`, `ConfigsEditor.vue`, `src/js/src/builder/configs.js` |
| Generated files (never edit) | `src/js/src/builder/schema/*.json`, `docs/content/builder/error-codes.md` |

## Build rules

### Serving Builder's files

The server sends the files that only the Builder loads compressed (Brotli
or gzip, as `Accept-Encoding` weighs them, Brotli on a tie), cached for one
year as `immutable`. The plugin `src/js/plugins/builder-assets.js` starts
from the `src/views/Builder.vue` chunk and leaves out files that the rest of
the UI also loads. It writes `.br` and `.gz` copies when they are smaller,
and lists the files in `builder-assets.json` beside `index.html`.

- If you rename or move `Builder.vue`, update the `view` that
  `src/js/vite.config.js` gives the plugin. The build fails when it finds
  no chunk.
- Without the list, the server sends every file uncompressed and logs a
  warning at start.
- `PHENIX_BROTLI_QUALITY` (0 to 11, default 9) sets the Brotli quality.
  The Docker and Podman images use 11. On the 7 Builder files (3.1 MB),
  quality 11 saves 74 kB over 9 and adds about 3 s to each build. User docs:
  [Compressed files](https://phenix.sceptre.dev/latest/builder/administration/#compressed-files).

### The chunk the rest of the UI loads

The rest of the UI loads three files from `src/builder/`: `session.js`
(from the app store, for login and logout), `focusMode.js` (from
`App.vue`) and `configs.js` (from the Configs page). Their imports from
`src/builder/` go into a chunk that the server sends uncompressed. Keep
these imports small: no `api.js`, decoder or validator. A test in
`session.test.js` checks the imports of `session.js` and `focusMode.js`.

### Names in the UI code

A name follows the button label, and a conversion keeps the server's word.
Import (a draft from a config) is `dialogs/ImportDialog.vue`, dialog key
`import`, ids and test ids `import-…`. It calls `store.generate`
(`POST /builder/generate`), then `store.openImported`. Upload is
`dialogs/UploadDialog.vue` (`upload`, `upload-…`), and it converts a legacy
diagram with `store.convertLegacy` (`POST /builder/legacy`). Download is
`dialogs/DownloadDialog.vue` (`download`, `download-…`).

Import and Upload both have a file, an error and a submit control. A test
finds the dialog by its title before it uses an id.

Gotcha: `openImported` decodes the diagram (`openableDocument`) before it
detaches the open draft, so a refused diagram leaves the open draft as it
was. It drops an answer that arrives after the dialog closed or after a
later submit.

### Node positions and the grid

The canvas draws every node at its document position. While Vue Flow's
`snapToGrid` is on, its NodeWrapper snaps each node that it mounts. Thus
`BuilderCanvas.vue` never binds that prop. It turns snapping on only for
the time of a drag, and only while the user setting
`builderSettings.snapToGrid` is on (`snapWhileDragging`, `stopSnapping`).
The setting is off by default, per user and per browser. A palette drop
goes to the grid, or to whole pixels with the setting off (`onGrid`, which
calls `snappedPoint` in `model.js`). Drafts can hold positions off the grid
(`builder-editing.spec.js` reloads one).

- The model places nodes with `{snap}` (`snapStep` and `snappedPoint` in
  `model.js`). `freeSpot`, `setParent`, `connectNodes`, `updateNode` and
  `applyFormData` take it, and the store gives `placing()`. Line points
  snap with the same step (`nodes/LineNode.vue`).
- `nodeDragThreshold` is 0. Vue Flow starts a node drag on the press, and
  the nodes stay under the pointer for the whole drag. Thus each click on
  a node arrives as a drag.
- A drag is a click while the pointer stays in the slop of the press:
  `CLICK_SLOP_PX` (4 screen pixels) for a mouse, `TOUCH_SLOP_PX` (8) for a
  finger or a pen, at any zoom (`pressOf`, `endedNear` and `dragIsClick`
  in `clickSlop.js`). `onNodeDrag` sets `gesture.strayed` when the pointer
  leaves the slop, so the drag stays a drag if the pointer comes back. A
  click moves nothing and selects the node. A selection-frame drag that is
  a click selects nothing.
- `onNodeDragStop` rounds positions to whole pixels. `drawAsSaved` draws
  the nodes and their groups back at the document positions when the drag
  is a click, moves nothing, or `store.moveNodes` refuses it (as during a
  save conflict).
- Shift and an arrow key call `store.holdNudge`, and Alt, Shift and an
  arrow key call `store.holdResize`, on every keydown and repeat. They set
  `store.doc` from the gesture base (`store.keyGesture`, markRaw) with no
  history entry, snapshot or announcement. `store.endKeyGesture` commits
  the gesture as one entry and one announcement. The canvas calls it on
  the keyup of an arrow key, Shift or Alt, and on focusout, window blur,
  pointerdown, unmount and any other keydown. `commit`, `undo` and `redo`
  call it first. A gesture whose diagram another document or a restore
  replaced ends without an entry. `stampEntry` moves the gesture base to
  the stamped diagram. A single press also commits at its keyup: the
  autosave queue never merges commits, so a commit at the first keydown
  makes each held key two undo steps. Test: `canvas-held-keys.test.js`.

## Tests

- Go, from `src/go`: `go test ./api/builder ./types/builder ./store ./web
  ./web/rbac`. The `api/builder` and `web` tests share the in-memory
  record store `store/recordtest/memrecord`, with hooks (`BeforeCreate`,
  `BeforeUpdate`, `FailDelete`, `FailPrefixDelete`, `Stamp`). A test that
  edits stored records uses its methods (`Count`, `Keys`, `SetValue`,
  `Drop`), not its fields.
- Go role fixtures (`web/builder_fixtures_test.go`): `builderUserRole`
  (as the built-in Builder role), `builderOnlyRole` (no config
  permission), `builderConfigsOnlyRole` (every config verb, no Builder),
  `nonBuilderRole(policies…)` and `builtinRole(t, name)` (a role of
  `api/config/default`). `runBuilderAccessCases` runs a table of callers
  against a route. `TestBuilderRoutesNeedTheBuilder` checks that every
  Builder route refuses no identity, a configs-only role and Global Viewer
  with 403.
- JS unit tests, from `src/js`: `npx vitest run test/builder`. `npm test`
  runs two Vitest projects (`src/js/vite.config.js`):
  - `contract` runs the `*.contract.test.js` suites of pure functions in
    shared workers (`npm run test:contract`). They must pass in any order,
    and CI runs them with `--sequence.shuffle`.
  - `unit` runs each other file in a fresh worker. A suite that mocks a
    module, stubs a global, or leaves storage, timers or module state
    behind goes in `unit`.

  `test/builder/template-links.test.js` tests template copies, and
  `library-render.test.js` tests the library UI.
- Browser: the `builder*.spec.js` Playwright specs in `src/js/e2e/tests/`
  need a running server. `src/js/e2e/README.md` gives the setup, the tags,
  the CI jobs, the live region and `toHaveAnnounced`, the shared icon and
  template libraries, and the needs of `builder-files.spec.js`
  (`E2E_BASE_DIR`) and `builder-multi-user.spec.js` (`E2E_MULTI_USER=1`).
  Not in the README:
  - With authentication off, every test is `global-admin`. All tests also
    share one layer set library, and the built-in layer sets are
    read-only. A test of the default layer set answers the
    `layersets/default` routes itself, because a real default changes the
    blank diagrams of parallel tests.
  - `builder-multi-user.spec.js` checks that users of the built-in Builder
    role list, open, change and delete every draft, template, icon and
    layer set, and that Global Viewer has no Builder. `userMaker`
    (`builder-support.js`) makes the users and deletes the library items
    that they made.
  - Other helpers in `builder-support.js`: `runFromPalette(title)` (a
    palette command by keyboard), `inspectorButton(action)` (`connect` or
    `regroup` in the Inspector header) and `seedLayerSet()`. The `tracker`
    fixture also deletes the layer sets that a test or its page adds.
- CI runs e2e on Linux (Enter, not Return) and on slower machines.
