# Builder

The Builder is the phenix web topology editor, built on Vue Flow. Every
`phenix ui` serves it at `/builder` and its REST API below
`/api/v1/builder/`. RBAC is the only control: no setting turns it on or
off. The unix socket of `phenix ui` does not serve the Builder routes.
There is no `phenix builder` command, so scripts use the REST API.

Read this file for any Builder task, including the `builder` `use`
permission, the `builder-doc` annotation and Builder files. Then read the
reference that the [routing table](#routing-table) names.

The user docs hold UI text, rules, limits and messages. These references
hold code locations, contracts, invariants, tests and gotchas, each fact in
one reference only.

sandialabs/sceptre-phenix#442 deleted the legacy Builder (mxGraph). Older
releases still have it. Its topologies keep `builder-xml`, and Import
converts them ([Legacy conversion](builder/sources.md#legacy-conversion)).

## User docs

The source is `docs/content/builder/`, and the routing table names the
page for each area.
[Overview](https://phenix.sceptre.dev/latest/builder/) is `index.md`.
The site (<https://phenix.sceptre.dev/latest/builder/>) shows released
docs only.
The UI links only to the site root (`HELP_URL` in `builder/help.js`).

## Architecture

- **Browser:** `src/js/src/views/Builder.vue` is the page,
  `src/js/src/components/builder/` holds the components, and
  `src/js/src/builder/` holds state and logic. A Pinia store (`store.js`)
  wraps a pure document model (`model.js`). A browser queue sends saves.
- **REST:** `src/go/web/builder*.go` serves `/api/v1/builder` and the
  Builder schemas. `web/builder.go` holds the routes and the authorization
  model.
- **Service:** `src/go/api/builder/` owns drafts, snapshots, published
  documents, the icon, template and layer set libraries, and preflight.
- **Document:** `src/go/types/builder/` defines the `builder/v1` document,
  its validation, generation from configs, legacy conversion, and the
  Topology that a publish writes. A shared test corpus holds the JS
  validator to Go.
- **Records:** drafts, published documents and the libraries are records in
  the phenix store, apart from configs
  ([Storage](builder/saving.md#storage)).

Only Publish writes Topology and Experiment configs. It also adds the
topology to the `topology` annotation of each Scenario that the document
lists. Only the Scenarios dialog stores a Scenario config.

## Rules

- After a change to `types/builder/` or to `types/version/schemas/`, run
  `make generate` (or `make generate-builder-schema`) in `src/go`. It
  writes the committed schema bundle and the error code table. CI fails
  when either is stale.
- A route change updates `src/go/web/public/docs/openapi.yml`. A Go test
  checks that every Builder route is in it.
- A new validation rule goes in Go and in JS, with a code and a case in the
  shared corpus ([document.md](builder/document.md#keys-and-validation)).
- Keep the editor accessible by keyboard and screen reader (WCAG 2.2 AA).
  Focus never falls to `<body>`. A dialog returns focus to its opener. The
  shared live region announces changes.
- If you move `Builder.vue`, update the build plugin settings
  ([Serving Builder's files](builder/code-and-tests.md#serving-builders-files)).
- Keep the modules that the rest of the UI loads small
  ([The chunk the rest of the UI loads](builder/code-and-tests.md#the-chunk-the-rest-of-the-ui-loads)).
- UI code names follow button labels
  ([Names in the UI code](builder/code-and-tests.md#names-in-the-ui-code)).
- A button that acts on one item (Edit, Delete, Publish, Duplicate, Open,
  View, Download) is `BuilderActionButton.vue`, never a hand-styled button
  ([Item actions](builder/editor.md#item-actions)).
- Vue Flow never snaps nodes when they mount
  ([Node positions and the grid](builder/code-and-tests.md#node-positions-and-the-grid)).
- When behavior or UI text changes, update the user docs page. When a
  contract or gotcha changes, update this file or the area reference.

## Routing table

Code paths are relative to `src/go/` (Go) and `src/js/src/` (JS).

| Task or area | Read | User docs | Main code |
|---|---|---|---|
| Routes, authorization (`builder` `use`), the Builder role, ETags, error codes and bodies | [api.md](builder/api.md) | [Administration](https://phenix.sceptre.dev/latest/builder/administration/), [Error Codes](https://phenix.sceptre.dev/latest/builder/error-codes/) | `web/builder.go`, `web/rbac/migrations.go`, `types/builder/codes.go`, `api/config/default/builder.yml` |
| Document keys, Go and JS validation, the generated schema, provenance, layers and matchers, network subnets and gateways | [document.md](builder/document.md) | [Building a Diagram](https://phenix.sceptre.dev/latest/builder/diagrams/) | `types/builder/document.go`, `validate.go`, `layerset.go`, `schema.go`, `builder/validate.js`, `decode.js`, `layerMatchers.js`, `addressing.js` |
| Editor and drafts page: opening, Inspector, Node Editor, layers editor, Scenarios dialog, notes, layouts, canvas, settings, keys, Go to, bulk actions, item actions, browser storage | [editor.md](builder/editor.md) | [The Editor](https://phenix.sceptre.dev/latest/builder/editor/), [Drafts](https://phenix.sceptre.dev/latest/builder/drafts/), [Building a Diagram](https://phenix.sceptre.dev/latest/builder/diagrams/) | `views/Builder.vue`, `components/builder/`, `builder/store.js`, `model.js`, `settings.js`, `commands.js`, `layouts/`, `issues.js`, `bulk.js`, `listSelection.js`, `itemActions.js` |
| Snapshots, the browser queue, merge on conflict, store records, the draft limit, storage cleanup | [saving.md](builder/saving.md) | [Drafts](https://phenix.sceptre.dev/latest/builder/drafts/) | `api/builder/service.go`, `content.go`, `cleanup.go`, `web/builder_cleanup.go`, `store/bolt_record.go`, `builder/autosave.js`, `merge.js`, `tabs.js` |
| Import, includes, copy and combine, legacy conversion, packages, Topology YAML, draw.io and GEXF | [sources.md](builder/sources.md) | [Import, Upload and Download](https://phenix.sceptre.dev/latest/builder/import-upload-download/), [Legacy Builder](https://phenix.sceptre.dev/latest/builder/legacy/) | `types/builder/generate.go`, `detach.go`, `legacy.go`, `package.go`, `web/builder_sources.go`, `builder_legacy.go`, `builder_package.go` |
| Publish, update rules and refusals, scenarios, host pins, the dry run, preflight | [publishing.md](builder/publishing.md) | [Publishing](https://phenix.sceptre.dev/latest/builder/publishing/), [The Editor](https://phenix.sceptre.dev/latest/builder/editor/) (Preflight) | `web/builder_publish.go`, `builder_preview.go`, `builder_preflight.go`, `api/builder/publish.go`, `changes.go`, `hostpins.go`, `preflight.go`, `builder/publish.js` |
| `builder-doc`, Builder files, published documents, `phenix config create` | [published-documents.md](builder/published-documents.md) | [Administration](https://phenix.sceptre.dev/latest/builder/administration/) | `api/builder/published.go`, `config_hook.go`, `file.go`, `web/builder_documents.go`, `store/types.go`, `cmd/config.go` |
| Node templates and their copies, template files, server collections, layer sets, built-in icons, the icon library | [templates-and-icons.md](builder/templates-and-icons.md) | [Node Templates](https://phenix.sceptre.dev/latest/builder/templates/) | `types/builder/layersetfile.go`, `api/builder/templates.go`, `templatefiles.go`, `layersets.go`, `icons.go`, `web/builder_templates.go`, `builder_layersets.go`, `builder_icons.go`, `builder/templates.js`, `layerSetStore.js`, `values.js` |
| Code locations, build rules, tests | [code-and-tests.md](builder/code-and-tests.md) | | |
