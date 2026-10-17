# Working on Builder code

Part of the [Builder references](../builder.md). Where the code is, the
build rules in full, and how to test.

## Where the code is

| Area | Files |
|---|---|
| Document model, validation, generation, publishing to configs, schemas | `src/go/types/builder/` (`document.go`, `validate.go`, `schema.go`, `generate.go`, `detach.go`, `topology.go`, `yaml.go`, `codes.go`, `customicons.go`, `template.go`, `templatefile.go`, `layerset.go`, `layerset_schema.go`, `layersetfile.go`, `package.go`, `legacy_xml.go`, `legacy.go`) |
| Drafts, snapshots, published documents, libraries, preflight | `src/go/api/builder/` (`service.go`, `published.go`, `chunks.go`, `limits.go`, `icons.go`, `templates.go`, `templatefiles.go`, `layersets.go`, `scope.go`, `config_hook.go`, `file.go`, `publish.go`, `changes.go`, `preflight.go`) |
| HTTP routes and authorization | `src/go/web/builder*.go` (see [api.md](api.md#routes)). `builder_assets.go` serves the editor's files |
| Builder role and its start-up check | `src/go/api/config/default/builder.yml`, `src/go/web/rbac/migrations.go`, `src/go/web/init.go` |
| `builder-doc` codec | `src/go/store/types.go` |
| Record store (BoltDB) | `src/go/store/*record*.go` |
| `phenix config create` and Builder files | `src/go/cmd/config.go` |
| Page and components | `src/js/src/views/Builder.vue`, `src/js/src/components/builder/` (`BuilderDrafts.vue`, `BuilderTemplates.vue`, `BuilderLayerSets.vue`, `BuilderBusyButton.vue`, `clickSlop.js`, canvas, `inspector/`, `dialogs/`, `layers/`, `nodes/`) |
| State and logic | `src/js/src/builder/` (`store.js`, `model.js`, `settings.js`, `autosave.js`, `merge.js`, `tabs.js`, `api.js`, `commands.js`, `commandSearch.js`, `keymap.js`, `layouts/`, `publish.js`, `scenarios.js`, `issues.js`, `validate.js`, `decode.js`, `exporters.js`, `gexf.js`, `package.js`, `icons.js`, `iconLibrary.js`, `templates.js`, `layerMatchers.js`, `layerSetStore.js`, `layerSetFile.js`, `layerLook.js`, `addressing.js`, `formOutline.js`, `bulk.js`, `listSelection.js`, `nodeNotes.js`) |
| Configs page links | `src/js/src/components/configs/ConfigsList.vue`, `ConfigsEditor.vue`, `src/js/src/builder/configs.js` |
| Generated files (never edit) | `src/js/src/builder/schema/*.json`, `docs/content/builder/error-codes.md` |

## Build rules

### Serving Builder's files

The server serves the files only Builder loads compressed (Brotli or gzip,
as `Accept-Encoding` weighs them, Brotli on a tie), cached for a year as
`immutable`. The plugin `src/js/plugins/builder-assets.js` starts from the
`src/views/Builder.vue` chunk, leaves out files the rest of the UI also
loads, writes `.br` and `.gz` copies when they are smaller, and lists the
files in `builder-assets.json` beside `index.html`.

- Renaming or moving `Builder.vue` means updating the `view` passed to the
  plugin in `src/js/vite.config.js`. The build fails when it finds no
  chunk.
- Without the list, the server serves every file uncompressed and logs a
  warning at startup.
- `PHENIX_BROTLI_QUALITY` (0 to 11, default 9) sets the Brotli quality. The
  Docker and Podman images use 11. On Builder's 7 files (3.1 MB), quality
  11 saves 74 kB over 9 and adds about 3 s to each build. User docs:
  [Compressed files](https://phenix.sceptre.dev/latest/builder/administration/#compressed-files).

### The chunk the rest of the UI loads

The rest of the UI loads `src/builder/api.js` to send saves left unsent.
What it imports from `src/builder/` lands in a chunk the server sends
uncompressed. Keep the decoder and validator out of it: the size limit it
shares with `decode.js` lives in `limits.js`, which imports nothing. A test
in `api.test.js` checks this.

### Names in the UI code

A name follows the button's label, and a conversion keeps the server's
word. Import (a draft from a config) is `dialogs/ImportDialog.vue`, dialog
key `import`, ids and test ids `import-…`. It calls `store.generate`
(`POST /builder/generate`) and then `store.openImported`. Upload is
`dialogs/UploadDialog.vue` (`upload`, `upload-…`), and a legacy diagram
goes through `store.convertLegacy` (`POST /builder/legacy`). Download is
`dialogs/DownloadDialog.vue` (`download`, `download-…`). Import and Upload
both have a file, an error and a submit control, so a test finds the
dialog by its title before it uses an id.

Gotcha: `openImported` decodes the diagram (`openableDocument`) before it
detaches the open draft, so a refused diagram leaves the open draft as it
was. An answer that arrives after the dialog closed, or after a later
submit, is dropped.

### Node positions and the grid

The canvas draws every node where the document puts it. While Vue Flow's
`snapToGrid` is on, its NodeWrapper snaps each node it mounts. So
`BuilderCanvas.vue` never binds that prop. It turns snapping on only from
the start to the end of a drag, and only while the user's setting
`builderSettings.snapToGrid` is on (`snapWhileDragging`, `stopSnapping`).
The setting is off by default, per user and per browser. The document has
no `grid.snap` (see [document.md](document.md#keys-and-validation)). A
palette drop goes to the grid, or to whole pixels with the setting off
(`onGrid`, which calls `snappedPoint` in `model.js`). Drafts can hold
positions off the grid (`builder-editing.spec.js` reloads one).

- The model places nodes with `{snap}` (`snapStep` and `snappedPoint` in
  `model.js`): `freeSpot`, `setParent`, `connectNodes`, `updateNode` and
  `applyFormData` take it, and the store passes `placing()`. A line's
  points snap with the same step (`nodes/LineNode.vue`).
- `nodeDragThreshold` is 0, so Vue Flow starts a node drag on the press,
  and the nodes stay under the pointer for the whole drag, with or without
  the grid. Each click on a node therefore comes as a drag.
- A drag is a click while the pointer stays within the slop of the press:
  `CLICK_SLOP_PX` (4 screen pixels) for a mouse, `TOUCH_SLOP_PX` (8) for a
  finger or a pen (`pressOf`, `endedNear` and `dragIsClick` in
  `clickSlop.js`), at any zoom, with or without the grid. `onNodeDrag`
  sets `gesture.strayed` once the pointer leaves the slop, so the drag
  stays a drag even when the pointer comes back. A click moves nothing and
  selects the node. A selection-frame drag that is a click selects nothing.
- `onNodeDragStop` rounds positions to whole pixels. When the drag is a
  click, moves nothing, or `store.moveNodes` refuses it (as while a
  save conflict is resolved), `drawAsSaved` draws the nodes and
  their groups back at the document's positions.

## Tests

- Go, from `src/go`: `go test ./api/builder ./types/builder ./store ./web
  ./web/rbac`. The `api/builder` and `web` tests share the in-memory record
  store `store/recordtest/memrecord`, with hooks (`BeforeCreate`,
  `BeforeUpdate`, `AfterWrite`, `FailDelete`, `FailPrefixDelete`, `Stamp`).
  Tests that edit stored records use its methods (`Count`, `Keys`,
  `SetValue`, `Drop`, `RewriteLocked`), not its fields.
- Go fixtures for roles are in `web/builder_fixtures_test.go`:
  `builderUserRole` (the Builder and every config verb, as the built-in
  Builder role), `builderOnlyRole` (the Builder and no config permission),
  `builderConfigsOnlyRole` (every config verb, no Builder),
  `nonBuilderRole(policies…)` and `builtinRole(t, name)` (a role of
  `api/config/default`). `builderAccessCallers` builds the permission
  matrix of a route that needs only `builder` `use`: no identity, a
  configs-only role and Global Viewer get 403, and a Builder-only role and
  Global Admin are allowed.
- Template copies are tested in `test/builder/template-links.test.js`, and
  the library UI in `library-render.test.js`.
- Unit, from `src/js`: `npx vitest run test/builder`. `npm test` runs two
  Vitest projects (`src/js/vite.config.js`). `contract` holds the suites of
  pure functions in shared workers (`npm run test:contract`), and they must
  pass in any order (`npx vitest run --project contract --sequence.shuffle`).
  `unit` runs each other file in a fresh worker. A suite that mocks a
  module, stubs a global, or leaves storage, timers or module state behind
  goes in `unit`.
- Browser: the `builder*.spec.js` Playwright specs in `src/js/e2e/tests/`
  need a running server. `src/js/e2e/README.md` has the setup, the tags
  (`@known-defect`, `@cross-browser`, `@axe`) and how CI splits the suite.
  Tag `@axe` only on full axe scans: CI runs them in a job of their own.
  - With authentication off, every test is `global-admin`, and all tests
    share the one template library and the one layer set library. A test
    selects and deletes only what it made, and deletes any icon it
    uploads. The built-in templates and layer sets are read only. A test
    of the default layer set answers the `layersets/default` routes
    itself, because a real default would change every blank diagram of
    the tests that run meanwhile.
  - `builder-multi-user.spec.js` needs sign-in on (a JWT signing key and
    an admin user) and `E2E_MULTI_USER=1`. It makes users of the built-in
    Builder role, and a Global Viewer (`userMaker` in
    `builder-support.js`), and checks that every Builder user lists,
    opens, changes and deletes every draft, template, icon and layer set,
    and that a role without `builder` `use` has no Builder. CI runs it in
    the job "E2E Builder multi-user" (`--jwt-signing-key e2e-multi-user`).
  - Helpers in `builder-support.js`: `runFromPalette(title)` runs a
    command from the palette with the keyboard, `inspectorButton(action)`
    finds a button of the Inspector's header (`connect` or `regroup`),
    `seedTemplates()` and `changeTemplate()` add and change library
    templates through the API, and `seedLayerSet()` adds a layer set. The
    `tracker` fixture deletes the templates, collections and layer sets
    that a test or its page adds. `userMaker` deletes the library items
    its users made.
  - `builder-files.spec.js` writes below the server's base directory. It
    needs the server on the same machine and `E2E_BASE_DIR` set to its
    `--base-dir.phenix`. Without it, the spec skips.
- Announcements: a spec that does not test what screen readers hear may set
  `test.use({ announceHold: 100 })` to shorten the live region's 750 ms
  hold. Assert announcements with `expect(builder).toHaveAnnounced(text)`,
  which waits in a log of everything the region showed. Do not read the
  region's current text, because the next message can replace it first.
  Shared helpers are in `builder-support.js`.
- CI runs e2e on Linux (Enter, not Return) and on slower machines.
