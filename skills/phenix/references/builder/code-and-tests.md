# Working on Builder code

Part of the [Builder references](../builder.md). Read this file before
changing any file it lists, together with the [rules](../builder.md#rules)
every Builder change follows; four of them are given in full below.

## Where the code is

| Area | Files |
|---|---|
| Document model, generation, publishing to configs, validation, JSON Schema, YAML reading | `src/go/types/builder/` (`document.go`, `generate.go`, `detach.go` (combine, copy), `topology.go`, `validate.go`, `schema.go`, `yaml.go`, `customicons.go`, `template.go`, `templatefile.go` and `templatefile_schema.go` (template files), `legacy_xml.go` and `legacy.go` (legacy conversion)) |
| Drafts, snapshots, sharing, published documents, libraries, limits | `src/go/api/builder/` (`service.go`, `shares.go`, `published.go`, `chunks.go`, `limits.go`, `validate.go`, `icons.go`, `templates.go`, `templatefiles.go` (the server collections read at start), `scope.go`; `config_hook.go` checks a topology's `builder-doc` and removes a deleted or renamed topology's documents; `file.go` reads Builder files; `publish.go` publishes a document as a topology for the CLI, and `ReplaceLegacyDiagram`; `changes.go` says what a publication changes, `DescribePublishChanges`; `preflight.go` makes the preflight checks) |
| Built-in Builder role and its start-up check | `src/go/api/config/default/builder.yml`, `src/go/web/rbac/migrations.go` (`EnsureBuilderRolePermissions`), `src/go/web/init.go` |
| `builder-doc` codec (nested in JSON and YAML, a string in memory and in the store) | `src/go/store/types.go` |
| `phenix builder publish`, and `phenix config create` recognizing Builder documents | `src/go/cmd/builder.go`, `src/go/cmd/config.go` |
| `phenix builder drafts` and `phenix builder templates`, the REST client they share | `src/go/cmd/builder_drafts.go`, `src/go/cmd/builder_templates.go`, `src/go/cmd/builder_client.go` |
| Record store for drafts (BoltDB and etcd, etcd compaction) | `src/go/store/*record*.go`, `src/go/store/etcd_record_compact.go` |
| HTTP routes, authorization and RBAC | `src/go/web/builder*.go` (`builder.go` holds the authorization model, the routes and the response headers; `builder_legacy.go`, `builder_icons.go`, `builder_templates.go`, `builder_experiments.go` (the `experiment` link); `builder_preview.go` (the dry run of a publication); `builder_preflight.go` (the preflight route and what it reads); `builder_assets.go` serves the editor's files, which `src/js/plugins/builder-assets.js` compresses in the UI build) |
| Editor page and drafts landing | `src/js/src/views/Builder.vue`, `src/js/src/components/builder/BuilderDrafts.vue`, `BuilderTemplates.vue` (the Node Templates tab), `BuilderBulkBar.vue`, `BuilderBulkSummary.vue`, `BuilderHeaderButtons.vue` (the buttons both headers share) |
| Configs page links | `src/js/src/components/configs/ConfigsList.vue`, `ConfigsEditor.vue`, `src/js/src/builder/configs.js` |
| Editor components | `src/js/src/components/builder/` (canvas, Inspector, outline, toolbar, side columns, `BuilderSignIn.vue`, dialogs, nodes, edges) |
| Editor state and logic | `src/js/src/builder/` (`store.js`, `model.js`, `autosave.js`, `idb.js`, `tabs.js`, `session.js`, `signin.js`, `panes.js`, `commands.js`, `keymap.js`, `layouts/`, `adapters/`, `publish.js`, `templates.js`, `templateFile.js`, `icons.js`, `iconLibrary.js`, `bulk.js`, `listSelection.js`, `nodeInfo.js`, `nodeNotes.js`, `grouping.js`, `groupingWorker.js`) |
| Generated schema bundle and error codes | `src/js/src/builder/schema/builder-v1.schema.json`, `src/js/src/builder/schema/codes.json` (from `types/builder/codes.go`) |

## Rules in full

### Serving Builder's files

The server serves the files only Builder loads compressed, with their
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
is served uncompressed and the server logs a warning at startup. gzip runs
at level 9, and Brotli at quality 9 unless `PHENIX_BROTLI_QUALITY` names
another whole number from 0 to 11; any other value fails the build. The
Docker image, the Podman image and the Debian package (built from the
Docker image) set it to 11 in their UI build stage; local builds and the
CI e2e build use 9. Measured on Builder's files (7 files, 3.1 MB):

| Brotli quality | Copies | Time to compress |
|---|---|---|
| 9 (default) | 786 kB | 0.07 s |
| 10 | 725 kB | 1.3 s |
| 11 (packages) | 711 kB | 3.0 s |

The files are cached for a year and renamed when they change, so a browser
downloads the 74 kB that quality 11 saves once per release, while 11 adds
about 3 s to every build.

### The chunk the rest of the UI loads

The rest of the UI loads `src/builder/api.js` to send saves left unsent, so
what it imports from `src/builder/` lands in a chunk the server sends
uncompressed. Keep the decoder and validator out of it: the document size
limit it shares with `decode.js` lives in `limits.js`, which imports
nothing. A test in `api.test.js` checks this.

### Names in the UI code

In the UI code, a name follows the button's label, and a conversion keeps
the server's word. Import (a draft from a Topology or Experiment config,
stored or in a config file) is `dialogs/ImportDialog.vue`, dialog key
`import`, ids and test ids `import-…`; it calls `store.generate`
(`POST /builder/generate`), which only checks the diagram, and then
`store.openImported`, which detaches the open draft and opens the
diagram, once the user accepts it: at once, or on Continue after
warnings. `openImported` decodes the diagram as `setDocument` does
(`openableDocument`) before it detaches anything, so a diagram it
refuses leaves the open draft, its autosave, owner and ETag as they
are. An answer that arrives after the dialog closed (a `closed` flag,
set by Cancel and, through `onScopeDispose`, when the dialog unmounts)
or after a later submit is dropped, as Upload drops a conversion.
Upload (a Builder document you have, a
published diagram, or a legacy diagram, which `store.convertLegacy` sends
to `POST /builder/legacy`) is `dialogs/UploadDialog.vue`, `upload`,
`upload-…`. Download (the open
diagram as a file) is `dialogs/DownloadDialog.vue`, `download`,
`download-…`; its files come from `exporters.js`, `gexf.js` and
`store.exportTopology` (`POST /builder/export/topology`). Import and
Upload both have a file, an error and a submit control, so a test finds
the dialog by its title before it uses an id.

### Node positions and the grid

The canvas draws every node where the document puts it, also on opening:
while Vue Flow's `snapToGrid` is on, its NodeWrapper snaps each node it
mounts. `BuilderCanvas.vue` therefore never binds the prop; it sets Vue
Flow's state from a node or selection drag's start to its end, and only
while `grid.snap` is on (`snapWhileDragging`, `stopSnapping`), and a
palette drop snaps itself (`onGrid`). Shift and an arrow key move 10 px
without snapping, so a draft holds positions off the grid, as layouts
give too (`builder-editing.spec.js` reloads one).

## Tests

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
- Unit: `npx vitest run test/builder` from `src/js`. `npm test` runs two
  Vitest projects (`src/js/vite.config.js`): `contract`, the suites of pure
  functions (model, schema, validation, layout, codecs, the compression
  plugin), in shared workers, which `npm run test:contract` runs alone; and
  `unit`, every other file, each in a fresh worker. A suite that mocks a
  module, stubs a global, or leaves storage, timers or module state behind
  goes in `unit`. The contract suites pass in any order:
  `npx vitest run --project contract --sequence.shuffle` checks it.
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
