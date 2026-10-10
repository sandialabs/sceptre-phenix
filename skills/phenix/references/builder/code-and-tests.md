# Working on Builder code

Part of the [Builder references](../builder.md). Where the code is, the
build rules in full, and how to test.

## Where the code is

| Area | Files |
|---|---|
| Document model, validation, generation, publishing to configs, schemas | `src/go/types/builder/` (`document.go`, `validate.go`, `schema.go`, `generate.go`, `detach.go`, `topology.go`, `yaml.go`, `codes.go`, `customicons.go`, `template.go`, `templatefile.go`, `package.go`, `legacy_xml.go`, `legacy.go`) |
| Drafts, snapshots, shares, published documents, libraries, preflight | `src/go/api/builder/` (`service.go`, `shares.go`, `published.go`, `chunks.go`, `limits.go`, `icons.go`, `templates.go`, `templatefiles.go`, `scope.go`, `config_hook.go`, `file.go`, `publish.go`, `changes.go`, `preflight.go`) |
| HTTP routes and authorization | `src/go/web/builder*.go` (see [api.md](api.md#routes)). `builder_assets.go` serves the editor's files |
| Builder role and its start-up check | `src/go/api/config/default/builder.yml`, `src/go/web/rbac/migrations.go`, `src/go/web/init.go` |
| `builder-doc` codec | `src/go/store/types.go` |
| Record store (BoltDB, etcd) | `src/go/store/*record*.go`, `src/go/store/etcd_record_compact.go` |
| `phenix config create` and Builder files | `src/go/cmd/config.go` |
| Page and components | `src/js/src/views/Builder.vue`, `src/js/src/components/builder/` (`BuilderDrafts.vue`, `BuilderTemplates.vue`, canvas, Inspector, dialogs, nodes) |
| State and logic | `src/js/src/builder/` (`store.js`, `model.js`, `autosave.js`, `merge.js`, `tabs.js`, `api.js`, `commands.js`, `keymap.js`, `layouts/`, `publish.js`, `issues.js`, `validate.js`, `decode.js`, `exporters.js`, `gexf.js`, `package.js`, `icons.js`, `iconLibrary.js`, `templates.js`, `bulk.js`, `listSelection.js`, `nodeNotes.js`) |
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
the start to the end of a drag, and only while `grid.snap` is on
(`snapWhileDragging`, `stopSnapping`). A palette drop snaps itself
(`onGrid`). Drafts can hold positions off the grid (`builder-editing.spec.js`
reloads one).

## Tests

- Go, from `src/go`: `go test ./api/builder ./types/builder ./store ./web
  ./web/rbac`. The `api/builder` and `web` tests share the in-memory record
  store `store/recordtest/memrecord`, with hooks (`BeforeCreate`,
  `BeforeUpdate`, `AfterWrite`, `FailDelete`, `FailPrefixDelete`, `Stamp`).
  Tests that edit stored records use its methods (`Count`, `Keys`,
  `SetValue`, `Drop`, `RewriteLocked`), not its fields.
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
  - With authentication off, every test is `global-admin` on one shared
    library. A test selects and deletes only what it made, never changes
    the five built-in templates, and deletes any icon it uploads.
  - `builder-sharing.spec.js` needs sign-in on (a JWT signing key and an
    admin user) and `E2E_SHARING=1`.
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
