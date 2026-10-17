# REST API contracts

Part of the [Builder references](../builder.md). The route index, how the
routes authorize, ETags, error codes and error bodies.

User docs: [REST API](https://phenix.sceptre.dev/latest/builder/administration/#rest-api)
lists each route with its `configs` permission, and
[Permissions](https://phenix.sceptre.dev/latest/builder/administration/#permissions)
describes the `builder` `use` permission and the Builder role.
`src/go/web/public/docs/openapi.yml` (served at `/docs/`) describes every
request and response. `TestBuilderRoutesDocumented` in `web/builder_test.go`
fails when a route or method is not in it.

## Routes

All routes are below `/api/v1`. `web/builder.go` registers them. Each row
names the handler file and the reference that holds the contract.

| Routes | Handler | Contract |
|---|---|---|
| `GET /schemas/builder/v1`, `/schemas/builder/templates/v1`, `/schemas/builder/package/v1`, `/schemas/builder/layersets/v1` | `builder.go`, `builder_layersets.go` | [document.md](document.md) |
| `/builder/drafts`, `/builder/drafts/{draft}`, `…/snapshots[/{snapshot}]`, `…/cursor` | `builder_drafts.go` | [saving.md](saving.md), [Drafts](#drafts) |
| `…/publish` (with `dryRun`) | `builder_publish.go`, `builder_preview.go` | [publishing.md](publishing.md) |
| `…/preflight` | `builder_preflight.go` | [publishing.md](publishing.md#preflight) |
| `/builder/sources`, `/builder/generate`, `/builder/legacy` | `builder_sources.go`, `builder_legacy.go` | [sources.md](sources.md) |
| `/builder/export/topology`, `/builder/package`, `/builder/package/resolve` | `builder_export.go`, `builder_package.go` | [sources.md](sources.md#downloads-and-packages) |
| `/builder/documents[/{document}]`, `/builder/topologies/{topology}/document` | `builder_documents.go` | [published-documents.md](published-documents.md) |
| `/builder/icons[/{icon}]` | `builder_icons.go` | [templates-and-icons.md](templates-and-icons.md#icon-library) |
| `/builder/templates`, `/builder/templates/items[/{template}]`, `/builder/templates/collections[/{collection}]`, `/builder/templates/delete` | `builder_templates.go` | [templates-and-icons.md](templates-and-icons.md#template-library) |
| `/builder/layersets[/{layerset}]`, `/builder/layersets/default` | `builder_layersets.go` | [templates-and-icons.md](templates-and-icons.md#layer-set-library) |

## Authorization

- Every Builder route needs one permission: `builder` `use`
  (`builderAuthorize` and `builderAllowed` in `web/builder.go`). It takes no
  resource names. Its holders read and change every draft, damaged draft,
  library template, collection, layer set, uploaded icon and Builder
  schema. There are no owner checks and no per-draft or per-object
  permissions. A refusal is 403 with `request.forbidden`, and a missing
  draft is 404.
- `builderAllowed` is a literal `role.Allowed("builder", "use")` call, so
  the RBAC policy generator (`web/rbac/known_policy_gen.go`) records it.
  `builderConfigAllowed` holds one literal call for each `configs` verb.
- A route that reads or writes phenix configs (publish, import, export,
  sources, packages, preflight, published documents) also needs the
  `configs` permission of its operation (`builderAuthorizeConfig`), and the
  permissions of each config it reads or writes. `builder` `use` never
  gives config access.
- The only server-side exception is an icon that the server added from its
  template files (`ServerIconOwner`): `bapi.MayChangeIcon` refuses its
  rename and delete with `ErrForbidden`, which the route answers with 403.
- The server has one default layer set (record `default`), which any
  Builder user reads and sets. The icon library has one limit for the
  whole server (`MaxIcons`, 2000 icons).
- The browser hides what the server would refuse: `store.canUseBuilder`
  (`builder` `use`) gates the nav link (`AppHeader.vue`), the view (a
  notice "Your role does not include the Builder." in `Builder.vue`) and
  the Configs page Builder links (`builderControl` in `ConfigsList.vue`).
  `store.canPublish` (`configs` `update`) gates Publish.
- The built-in role `Builder` is `api/config/default/builder.yml`: `builder`
  `use` plus its `configs` rule, which names `Topology/*`, `Scenario/*` and
  `Experiment/*`, never `*/*`: that would expose User and Role configs
  (password hashes and role changes). `rbac.EnsureBuilderRolePermissions`
  (`web/rbac/migrations.go`, called from `web.Init` at every start) creates
  the role when it is missing, and adds `builder` `use` to a role named
  `builder` or `Builder` and to its users when they lack it. It changes
  nothing else, so stored `builder-drafts`, `builder-icons` and
  `builder-templates` grants stay and nothing checks them. Global Viewer
  has no `builder` `use`, so it has no Builder.

### Drafts

Draft paths are `/builder/drafts/{draft}`: the ID alone, with no owner
segment. Draft responses carry `createdBy` and `lastModifiedBy`, for
provenance only. `POST /builder/drafts` with an `owner` key is 400 (strict
decoding), and `forkOf` is a draft ID. Responses also carry `experiment`:
an experiment the publication made that still exists, read from its
`builder-experiment` annotation, only for a caller with `experiments` `get`
on it. `GET /builder/drafts` answers `{drafts, damaged}`: every draft, the
latest changed first, and the drafts whose metadata this server cannot
read, which can only be deleted with their `etag`
(`Service.GetDamagedDraft`).

## ETags

- Every change to an existing draft needs `If-Match` with the quoted ETag
  of the last response. A missing or malformed tag is 400 and a stale one
  412. A 412 carries the current `ETag`.
- Draft responses also carry the tag in the body as `etag`. Clients read
  that one, because a compressing proxy can rewrite the header (`W/"3"`,
  `"3-gzip"`).
- Library templates, collections and layer sets have strong ETags
  `"<version>"`. Only the two template `PUT` routes and the layer set
  `PUT` need `If-Match`. A stale tag answers 412 with `template.item.stale`
  or `layerset.stale`.
- A change whose write succeeded but whose cleanup of old content failed
  answers its normal status, body and new `ETag`, plus a `Warning: 199`
  header that names the operation. The server logs the cause and never
  sends it. A failure would only make the client retry with a stale tag.

## Error codes

Every Builder problem has a stable code: dotted lowercase words, the first
of which names the subject (`node.hostname.duplicate`,
`publish.topology.exists`, `request.not-found`). The one registry is
`types/builder/codes.go` (`LookupCode`, constants
`Code…`), with a default severity and a description for each code.

- `make generate-builder-schema` writes the registry to
  `src/js/src/builder/schema/codes.json` and to the table in
  `docs/content/builder/error-codes.md`. Go tests fail when either is
  stale. The docs table is written and checked only where `docs/` exists
  (`BUILDER_SCHEMA_TESTS` in `src/go/Makefile`), because the Docker and
  Podman builds run `make generate` without it.
- A released code never changes its meaning. A new rule gets a new code in
  Go and in `validate.js` and `decode.js`, which report the same codes for
  the same rules. The shared corpus pins `code` for each case with an
  error. `validate.test.js` checks that each quoted dotted name in
  `validate.js` and `decode.js` is a code or a schema path, and that no
  code is built in a template string.

### Issues and error bodies

An issue (`bdoc.Issue`, OpenAPI `BuilderIssue`) is `{code, severity,
message, path?, nodeId?, edgeId?, networkId?, field?}`. `field` is the JSON
Forms path of a device field (`spec.network.interfaces.0.vlan`).
`bdoc.ErrorIssues(err)` finds the issues of any error. Strict decoding
reports an unknown key, or a value of the wrong type at a path the editor
checks, as an issue (`decodeIssues` in `types/builder/decodeissues.go`,
`DocumentError.issues` in `decode.js`). Other decoding refusals keep the
decoder's error.

- Every Builder route's error body has `code` (`builderHandler`,
  `builderCodedError` in `web/builder.go`). A body has `issues` for a
  package, document, template file or layer set that does not validate
  (`builderErrorCode`), and for what only Publish refuses. Other phenix routes have neither
  (`TestNonBuilderErrorsCarryNoCode`).
- Draft answers that a client must tell apart have `draft.*` codes:
  `draft.stale` (412), `draft.conflict` (409) and
  `draft.snapshot.current` (409). Other routes use `request.stale` and
  `request.conflict`.
- A method a Builder route does not take answers 405
  `request.method-not-allowed` with `Allow` (`builderMethodNotAllowed`).
- Publish `warnings` and `errors`, and the Topology YAML download's
  `warnings` and `publishBlockers`, are issues. `readIssues` in `api.js`
  reads them. `publishRefusal` in `publish.js` picks the form field by code
  (`REFUSAL_RULES`). Generation and legacy conversion warnings are plain
  text.
