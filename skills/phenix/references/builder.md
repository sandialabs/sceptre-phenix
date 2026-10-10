# Builder

The Builder is the web topology editor of phenix, built on Vue Flow. It is
on in every `phenix ui`, at `/builder`, with its REST API below
`/api/v1/builder/`. Nothing turns it on or off: RBAC is the only control.
The unix socket of `phenix ui` does not serve the Builder routes. There is
no `phenix builder` command: scripts use the REST API.

**Read this file for any Builder task**: its UI, its routes, the
`builder-drafts`, `builder-templates` and `builder-icons` RBAC resources,
its file formats, the `builder-doc` annotation, Builder files, or its code.
Then read the reference that the [routing table](#routing-table) names.

The user docs describe what the Builder does: UI text, rules, limits and
messages. This index and the references under `builder/` hold only what an
agent needs to change the code: where it is, contracts, invariants, tests
and gotchas. Each fact is in one reference only.

The legacy Builder (mxGraph, `builder-xml` topologies) was removed in
sandialabs/sceptre-phenix#442. The last commit with it is `a0aeaa4e`.
Topologies it saved still carry `builder-xml`, and Import converts them
(see [Legacy conversion](builder/sources.md#legacy-conversion)).

## User docs

The source is `docs/content/builder/`. The site is
<https://phenix.sceptre.dev/latest/builder/>, which shows released docs
only: a section this branch adds is there only after a release.

| Page | What it covers | Skill reference |
|---|---|---|
| [Overview](https://phenix.sceptre.dev/latest/builder/) | What the Builder does, the example lab, a quick start | — |
| [Drafts](https://phenix.sceptre.dev/latest/builder/drafts/) | Drafts page, saving, conflicts, Draft History, sharing, published diagrams, logout | [saving.md](builder/saving.md), [editor.md](builder/editor.md) |
| [The Editor](https://phenix.sceptre.dev/latest/builder/editor/) | Header, toolbar, canvas, Inspector, checks, Preflight, palette, keys, settings, accessibility | [editor.md](builder/editor.md), [publishing.md](builder/publishing.md#preflight) |
| [Building a Diagram](https://phenix.sceptre.dev/latest/builder/diagrams/) | Devices, switches, notes, groups, drawings, colors, icons, Purdue layers, layouts, scenarios, included topologies | [document.md](builder/document.md), [editor.md](builder/editor.md) |
| [Node Templates](https://phenix.sceptre.dev/latest/builder/templates/) | Templates, Restore, collections, template files, server collections, sharing | [templates-and-icons.md](builder/templates-and-icons.md) |
| [Import, Upload and Download](https://phenix.sceptre.dev/latest/builder/import-upload-download/) | Import options, copies, Upload, downloads, packages, `phenix config create` | [sources.md](builder/sources.md) |
| [Publishing](https://phenix.sceptre.dev/latest/builder/publishing/) | Publish, the preview, scenarios, what blocks publishing, updates | [publishing.md](builder/publishing.md) |
| [Administration](https://phenix.sceptre.dev/latest/builder/administration/) | Permissions, the Builder role, storage, etcd, template files, `builder-doc`, Builder files, REST API, troubleshooting | [api.md](builder/api.md), [published-documents.md](builder/published-documents.md) |
| [Error Codes](https://phenix.sceptre.dev/latest/builder/error-codes/) | Every error code (generated from `codes.go`) | [api.md](builder/api.md#error-codes) |
| [Legacy Builder](https://phenix.sceptre.dev/latest/builder/legacy/) | Converting `builder-xml` diagrams | [sources.md](builder/sources.md#legacy-conversion) |

The UI links only to the docs root (`HELP_URL` in `builder/help.js`).

## Architecture

- **Browser:** `src/js/src/views/Builder.vue` is the page,
  `src/js/src/components/builder/` its components, and `src/js/src/builder/`
  its state and logic: a Pinia store (`store.js`) over a pure document
  model (`model.js`). Saves go to the server through a queue kept in the
  browser.
- **REST:** `src/go/web/builder*.go` serves `/api/v1/builder` and the three
  Builder schemas. `web/builder.go` holds the routes and the authorization
  model.
- **Service:** `src/go/api/builder/` owns drafts, snapshots, shares,
  published documents, the icon and template libraries and preflight.
- **Document:** `src/go/types/builder/` defines the `builder/v1` document,
  its validation, generation from configs, legacy conversion, and the
  Topology a document publishes as. The JS validator matches Go through a
  shared test corpus.
- **Records:** drafts, published documents and the libraries are records
  in the phenix store, apart from configs (see
  [Storage](builder/saving.md#storage)).

Drafts save apart from configs. Only Publish writes Topology and Experiment
configs (and adds the topology to the `topology` annotation of each
Scenario the document lists). Only the Scenarios dialog stores a Scenario
config.

## Rules

- Run `make generate` (or `make generate-builder-schema`) in `src/go` after
  a change to `types/builder/` or to the config schemas in
  `types/version/schemas/`. It rewrites the committed schema bundle and the
  error code table, and CI fails when either is stale.
- A change to a route updates `src/go/web/public/docs/openapi.yml`. A Go
  test checks that every Builder route is in it.
- A new validation rule goes in Go and in JS, with a code and a case in the
  shared corpus ([document.md](builder/document.md#keys-and-validation)).
- Keep the editor keyboard and screen reader accessible (WCAG 2.2 AA):
  focus never falls to `<body>`, dialogs return focus to their opener, and
  changes are announced through the shared live region.
- Moving `Builder.vue` means updating the build's plugin settings
  ([Serving Builder's files](builder/code-and-tests.md#serving-builders-files)).
- Keep the decoder and validator out of the chunk the rest of the UI loads
  ([The chunk the rest of the UI loads](builder/code-and-tests.md#the-chunk-the-rest-of-the-ui-loads)).
- UI code names follow button labels
  ([Names in the UI code](builder/code-and-tests.md#names-in-the-ui-code)).
- Vue Flow never snaps nodes as they mount
  ([Node positions and the grid](builder/code-and-tests.md#node-positions-and-the-grid)).
- When behavior or UI text changes, update the user docs page, and this
  index or the reference for the area when a contract or gotcha changes.

## Routing table

Code paths are relative to `src/go/` (Go) and `src/js/src/` (JS).

| Task or area | Read | Main code |
|---|---|---|
| Routes, authorization, shares, the Builder role, ETags, error codes and bodies | [api.md](builder/api.md) | `web/builder.go`, `web/builder_shares.go`, `types/builder/codes.go`, `api/config/default/builder.yml` |
| Document keys, validation in Go and JS, the generated schema, provenance, Purdue levels | [document.md](builder/document.md) | `types/builder/document.go`, `validate.go`, `schema.go`, `builder/validate.js`, `decode.js` |
| Editor and drafts page: opening, Inspector, notes, layouts, canvas, keys, Go to, bulk actions, browser storage | [editor.md](builder/editor.md) | `views/Builder.vue`, `components/builder/`, `builder/store.js`, `model.js`, `layouts/`, `issues.js`, `bulk.js`, `listSelection.js` |
| Snapshots, the browser queue, merge on conflict, store records | [saving.md](builder/saving.md) | `api/builder/service.go`, `chunks.go`, `builder/autosave.js`, `merge.js`, `tabs.js` |
| Import, includes, copy and combine, legacy conversion, packages, Topology YAML and GEXF | [sources.md](builder/sources.md) | `types/builder/generate.go`, `detach.go`, `legacy.go`, `package.go`, `web/builder_sources.go`, `builder_legacy.go`, `builder_package.go` |
| Publish, its update rules and refusals, scenarios, the dry run, preflight | [publishing.md](builder/publishing.md) | `web/builder_publish.go`, `builder_preview.go`, `builder_preflight.go`, `api/builder/changes.go`, `preflight.go`, `builder/publish.js` |
| `builder-doc`, Builder files, published documents, `phenix config create` | [published-documents.md](builder/published-documents.md) | `api/builder/published.go`, `config_hook.go`, `file.go`, `web/builder_documents.go`, `store/types.go`, `cmd/config.go` |
| Node templates, template files, server collections, the icon library | [templates-and-icons.md](builder/templates-and-icons.md) | `api/builder/templates.go`, `templatefiles.go`, `icons.go`, `web/builder_templates.go`, `builder_icons.go` |
| Where the code is, build rules, tests | [code-and-tests.md](builder/code-and-tests.md) | |
