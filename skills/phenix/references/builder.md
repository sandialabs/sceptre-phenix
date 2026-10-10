# Builder

The Builder is phenix's web topology editor, built on Vue Flow. This file
is the index of its references: where it runs, how its parts fit, the rules
every Builder change follows, and which reference under `builder/` holds
the full text for a task.

**Read this file when** a task involves Builder: its drafts, sharing,
publishing, import, upload, download or generation, the conversion of legacy
diagrams, custom icons, node templates and their libraries, template files
(export, import, and the server collections read from
`--base-dir.builder-templates`), Builder packages (`/builder/package`, `/builder/package/resolve`),
its `/api/v1/builder/*`,
`/schemas/builder/v1`, `/schemas/builder/templates/v1` or
`/schemas/builder/package/v1` routes, the
`builder-drafts` and
`builder-templates` RBAC resources or the built-in Builder role, the Builder
document format (`builder/v1`), the `builder-doc` annotation, Builder files
named by `builder-doc.path`, or any Builder code
(see
[Working on Builder code](builder/code-and-tests.md)). The legacy
Builder of earlier releases (mxGraph, `builder-xml` topologies) was removed
in sandialabs/sceptre-phenix#442 (the last commit with it is `a0aeaa4e`);
topologies it saved still carry `builder-xml`, and the Builder converts
them (see [Legacy import](builder/sources.md#legacy-import)).

**Then read the reference the [routing table](#routing-table) names** for
the task. Each contract, limit, quoted message and route is in one
reference only; a task that crosses areas reads each of them.

Words, as the UI and the user docs use them: **Import** makes a draft from a
phenix config (stored, or a config file; `POST /builder/generate`);
**Upload** opens a file that is already a diagram (a Builder document, or a
legacy diagram through `POST /builder/legacy`), or a published diagram;
**Download** saves the open diagram as a file. The Builder is in every
`phenix ui`; nothing turns it on or off, and RBAC is the only control.

## Where it runs

The Builder, the Vue Flow topology editor, is on in every `phenix ui`, at
`/builder`, with its draft and document APIs under `/api/v1/builder`.
Scripts use the REST API, and people use the web UI.
The unix socket of `phenix ui` does not serve the Builder routes.
Drafts autosave separately from phenix configs; in the web UI and REST API
only the explicit Publish action creates or updates topology or experiment
configs and adds the topology to the `topology` annotation of the
document's scenarios, and only the Scenario dialog stores a Scenario
config (from an uploaded file, with `POST`/`PUT /configs`).

## Architecture

- **Browser:** `src/js/src/views/Builder.vue` is the page,
  `src/js/src/components/builder/` its components, and `src/js/src/builder/`
  its state and logic: a Pinia store (`store.js`) over a pure document model
  (`model.js`), saving to the server through a queue kept in the browser.
- **REST:** `src/go/web/builder*.go` serves `/api/v1/builder` and the three
  Builder schemas; `web/builder.go` holds the authorization model.
- **Service:** `src/go/api/builder/` owns drafts, snapshots, shares,
  published documents, the icon and template libraries and the preflight
  checks.
- **Document:** `src/go/types/builder/` defines the `builder/v1` document,
  its validation, generation from configs, legacy conversion, the Topology a
  document publishes as, and the JSON Schema committed as
  `src/js/src/builder/schema/builder-v1.schema.json`. The JS validator
  matches Go through a shared test corpus.
- **Records:** drafts, published documents and the libraries are records in
  the phenix store, apart from configs (see [Storage](builder/saving.md#storage)).

## Rules

- Run `make generate` (or `make generate-builder-schema`) in `src/go` after
  changing `types/builder/` or the config schemas in `types/version/schemas/`:
  it rewrites the committed schema bundle above, and CI fails when the bundle
  is stale.
- A change to a route updates `src/go/web/public/docs/openapi.yml`; a Go test
  checks that every Builder route is documented.
- Keep the editor keyboard and screen reader accessible (WCAG 2.2 AA): focus
  never falls to `<body>`, dialogs return focus to their opener, and changes
  are announced through the shared live region.
- The UI build compresses the files only Builder loads, and moving
  `Builder.vue` means updating the build's plugin settings
  ([Serving Builder's files](builder/code-and-tests.md#serving-builders-files)).
- The decoder and validator stay out of the chunk the rest of the UI loads
  through `src/builder/api.js`
  ([The chunk the rest of the UI loads](builder/code-and-tests.md#the-chunk-the-rest-of-the-ui-loads)).
- UI code names follow button labels, and conversions keep the server's
  word ([Names in the UI code](builder/code-and-tests.md#names-in-the-ui-code)).
- Vue Flow never snaps nodes as they mount; the canvas snaps only while
  dragging ([Node positions and the grid](builder/code-and-tests.md#node-positions-and-the-grid)).
- Keep these references current when Builder behavior changes: this index,
  and the reference its routing table names for the area.

## Routing table

Code paths are relative to `src/go/` (Go) and `src/js/src/` (JS).

| Task or area | Read | Main code |
|---|---|---|
| Opening diagrams, Inspector, notes, layouts, Sign in again, logout, header, toolbar, keys, palette, settings, side columns, minimap, Fit, lists of checks and Go to | [editor.md](builder/editor.md) | `views/Builder.vue`, `components/builder/`, `builder/store.js`, `model.js`, `commands.js`, `keymap.js`, `layouts/`, `nodeNotes.js`, `issues.js` |
| Drafts page: tabs, cards, bulk actions, keyboard model of selectable lists | [drafts-page.md](builder/drafts-page.md) | `components/builder/BuilderDrafts.vue`, `builder/bulk.js`, `listSelection.js` |
| Colors and styles, icon size, drawings, node tooltip, PNG and SVG limits, the custom icon library | [presentation-and-icons.md](builder/presentation-and-icons.md) | `types/builder/customicons.go`, `api/builder/icons.go`, `web/builder_icons.go`, `builder/icons.js`, `iconLibrary.js` |
| Node templates: built-in, diagram, library, sharing, server-wide, template files, server collections | [templates.md](builder/templates.md) | `types/builder/template.go`, `templatefile.go`, `api/builder/templates.go`, `templatefiles.go`, `web/builder_templates.go` |
| Draft shares, permissions, ETags on mutations, Builder RBAC resources, the Builder role | [sharing.md](builder/sharing.md) | `web/builder.go`, `api/builder/shares.go`, `api/config/default/builder.yml`, `web/rbac/migrations.go` |
| Snapshots, the browser queue and tabs, merge on conflict, Draft History, store records | [saving.md](builder/saving.md) | `api/builder/service.go`, `chunks.go`, `store/*record*.go`, `builder/autosave.js`, `merge.js`, `tabs.js` |
| Import, included topologies, combine and copy, uploaded configs, legacy conversion | [sources.md](builder/sources.md) | `types/builder/generate.go`, `detach.go`, `legacy.go`, `web/builder_sources.go`, `web/builder_legacy.go` |
| A document's scenarios, Scenarios dialog, Publish and its refusals, publish preview | [publishing.md](builder/publishing.md) | `web/builder_publish.go`, `web/builder_preview.go`, `api/builder/changes.go`, `builder/publish.js` |
| Preflight checks | [preflight.md](builder/preflight.md) | `api/builder/preflight.go`, `web/builder_preflight.go`, `builder/preflight.js` |
| Published documents after publish, delete and rename; the `builder-doc` annotation; Builder files | [published-documents.md](builder/published-documents.md) | `api/builder/published.go`, `config_hook.go`, `file.go`, `web/builder_documents.go`, `store/types.go` |
| Root keys, `metadata`, diagram notes, the generated schema, provenance | [document.md](builder/document.md) | `types/builder/document.go`, `validate.go`, `schema.go`, `builder/validate.js`, `decode.js` |
| Error codes, decoding refusals, issues, error bodies | [error-codes.md](builder/error-codes.md) | `types/builder/codes.go`, `decodeissues.go` |
| Topology YAML and GEXF downloads, Builder packages | [downloads-and-packages.md](builder/downloads-and-packages.md) | `types/builder/package.go`, `web/builder_package.go`, `builder/package.js`, `gexf.js` |
| `phenix config create` skipping or refusing Builder files | [published-documents.md](builder/published-documents.md#phenix-config-create-and-builder-files) | `cmd/config.go` |
| Every route and its permissions | [routes.md](builder/routes.md) | `web/builder*.go`, `web/public/docs/openapi.yml` |
| Where the code is, the rules in full, tests | [code-and-tests.md](builder/code-and-tests.md) | |
