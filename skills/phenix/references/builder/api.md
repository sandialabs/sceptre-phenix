# REST API contracts

Part of the [Builder references](../builder.md): routes, authorization,
ETags, error codes and error bodies.

`src/go/web/public/docs/openapi.yml` (served at `/docs/`) describes every
request and response. `TestBuilderRoutesDocumented` in `web/builder_test.go`
fails when a route or method is not in it. User docs:
[REST API](https://phenix.sceptre.dev/latest/builder/administration/#rest-api) (the `configs` permission of each
route) and [Permissions](https://phenix.sceptre.dev/latest/builder/administration/#permissions).

## Routes

All routes are below `/api/v1`. `web/builder.go` registers them.

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
| `POST /builder/cleanup` (with `dryRun`) | `builder_cleanup.go` | [saving.md](saving.md#storage) |

## Authorization

- Every Builder route needs `builder` `use` (`builderAuthorize` and
  `builderAllowed` in `web/builder.go`). The permission takes no resource
  names. Its holders read and change every draft, damaged draft, library
  template, collection, layer set, uploaded icon and Builder schema. There
  are no owner checks and no per-object permissions. A refusal is 403
  `request.forbidden`. A missing draft is 404.
- `builderAllowed` is a literal `role.Allowed("builder", "use")` call, so
  the RBAC policy generator (`web/rbac/known_policy_gen.go`) records it.
  `builderConfigAllowed` holds one literal call for each `configs` verb.
- A route that reads or writes configs (publish, import, export, sources,
  packages, preflight, published documents) also needs the `configs`
  permission of its operation (`builderAuthorizeConfig`), and the
  permissions of each config that it touches. `builder` `use` never gives
  config access.
- The one exception on the server: `bapi.MayChangeIcon` refuses a rename
  or delete of a server icon (`ServerIconOwner`) with `ErrForbidden`
  (403).
- The browser hides what the server refuses. `store.canUseBuilder`
  controls the nav link (`AppHeader.vue`), the view (`Builder.vue`) and
  the Configs page links (`builderControl` in `ConfigsList.vue`). Edit on
  a `builder-doc` topology opens the Builder only with `builder` `use`,
  else `ConfigsEditor.vue` shows an alert. `store.canPublish` (`configs`
  `update`) controls Publish.
- The built-in `Builder` role (`api/config/default/builder.yml`) has
  `builder` `use` and a `configs` rule for `Topology/*`, `Scenario/*` and
  `Experiment/*`. Never use `*/*`: it exposes User and Role configs
  (password hashes and role changes).
- At every start, `rbac.EnsureBuilderRolePermissions`
  (`web/rbac/migrations.go`, called from `web.Init`) creates the role when
  it is missing. It also adds `builder` `use` to a role named `builder` or
  `Builder`, and to its users. It changes nothing else, so stored
  `builder-drafts`, `builder-icons` and `builder-templates` grants stay
  unused. Global Viewer has no `builder` `use`.

### Drafts

- Draft paths are `/builder/drafts/{draft}`, with no owner segment.
  `createdBy` and `lastModifiedBy` are provenance only.
- `POST /builder/drafts` with an `owner` key is 400 (strict decoding).
  `forkOf` is a draft ID.
- `experiment` in a draft response names a live experiment that the
  publication made (from its `builder-experiment` annotation). Only a
  caller with `experiments` `get` on it gets it.
- `GET /builder/drafts` answers `{drafts, damaged, limits}`. `drafts` is
  every draft, latest change first. `damaged` lists drafts whose metadata
  this server cannot read. Only a delete with their `etag` works on them
  (`Service.GetDamagedDraft`). `limits.drafts` is `MaxDrafts`
  ([Storage](saving.md#storage)).

## ETags

- A change to an existing draft needs `If-Match` with the quoted ETag of
  the last response. A missing or malformed tag is 400. A stale tag is
  412, with the current `ETag`.
- Draft responses also carry the tag in the body as `etag`. Clients read
  the body field, because a compressing proxy can rewrite the header
  (`W/"3"`, `"3-gzip"`).
- Library templates, collections and layer sets have strong ETags
  `"<version>"`
  ([templates-and-icons.md](templates-and-icons.md#template-library)).
- Every Builder delete needs `If-Match` with the same rules
  (`builderIfMatch`, `builderStrongETag`). The tag that each delete names:
  - A draft or a snapshot: the draft's tag.
  - `DELETE /builder/layersets/{layerset}`: the set's `"<version>"`, or
    the `RevisionETag` of an unreadable record.
  - `DELETE /builder/icons/{icon}`: the icon's `etag`, the `RevisionETag`
    of its record, which each rename changes. Stale is 412
    `request.stale`.
  - `DELETE /builder/documents/{document}`: the document's `etag`
    (`builderDocumentETag`), a hash of the document ID and the topology's
    stored JSON. Any topology change changes it. Stale is 412
    `request.stale`. An unreadable record uses its `RevisionETag`.
  - `POST /builder/templates/delete`: `{id, etag}` for each item
    (`builderTemplateSelection.check`). One stale item refuses the whole
    request.
- `PUT /builder/layersets/default` names the tag of the choice
  (`LayerSetDefault.ETag`, `"0"` with no record), listed as `defaultEtag`.
- The UI handles a 412 on a delete as a stale save: it reads the list
  again and tells the user to check the item (`deletePublished`,
  `deleteLibraryItems`, `staleChecked` in `layerSetStore.js`,
  `iconLibrary.remove`).
- A draft save, cursor move, snapshot delete, draft delete, icon rename and
  icon delete each write in one store transaction, so none half succeeds.
- Some changes delete other content after their own write: a publish that
  deletes superseded documents, `DELETE /builder/documents/…` and
  `POST /builder/cleanup`. When that second step fails, the answer keeps
  its normal status and body and adds a `Warning: 199` header that names
  the operation. The server logs the cause and never sends it.
- A write that timed out on the client
  ([The browser queue](saving.md#the-browser-queue)) can still commit on
  the server. Thus every draft write carries `If-Match`, and a snapshot
  carries `opId`.

## Error codes

Every Builder problem has a stable code of dotted lowercase words. The
first word names the subject (`node.hostname.duplicate`,
`publish.topology.exists`, `request.not-found`). `types/builder/codes.go`
is the one registry (`LookupCode`, `Code…` constants). It gives each code a
default severity and a description.

- `make generate-builder-schema` writes the registry to
  `src/js/src/builder/schema/codes.json` and to the table in
  `docs/content/builder/error-codes.md`. Go tests fail when either is
  stale. The command writes and checks the docs table only where `docs/`
  exists (`BUILDER_SCHEMA_TESTS` in `src/go/Makefile`), because the Docker
  and Podman builds run `make generate` without `docs/`.
- A released code never changes its meaning, and a new rule gets a new
  code ([document.md](document.md#keys-and-validation)). The shared corpus
  pins `code` for each error case.
  `validate.contract.test.js` checks that each quoted dotted name in
  `validate.js` and `decode.js` is a code or a schema path. It also checks
  that no template string builds a code.

### Issues and error bodies

An issue (`bdoc.Issue`, OpenAPI `BuilderIssue`) is `{code, severity,
message, path?, nodeId?, edgeId?, networkId?, field?}`. `field` is the JSON
Forms path of a device field (`spec.network.interfaces.0.vlan`).
`bdoc.ErrorIssues(err)` finds the issues of any error. Strict decoding
reports an unknown key, or a wrong value type at a path that the editor
checks, as an issue (`decodeIssues` in `types/builder/decodeissues.go`,
`DocumentError.issues` in `decode.js`). Other decoding refusals keep the
decoder's error.

- Every Builder error body has `code` (`builderHandler`,
  `builderCodedError` in `web/builder.go`). A body has `issues` for an
  invalid package, document, template file or layer set
  (`builderErrorCode`), and for refusals that only Publish makes. Other
  phenix routes have neither (`TestNonBuilderErrorsCarryNoCode`).
- The client refuses an upload over `MAX_DOCUMENT_BYTES` (document or file
  text) or `MAX_REQUEST_BYTES` (body) before it sends it (`checkUploadSize`
  in `api.js`). It throws `TooLargeError` (`classifyError`: `too-large`).
  The server resets such a body before it reads all of it. Some browsers
  (Firefox) then report the reset connection, not the 413, which looks like
  an unreachable server.
- Draft answers that a client must tell apart have `draft.*` codes:
  `draft.stale` (412), `draft.conflict` (409) and
  `draft.snapshot.current` (409). Other routes use `request.stale` and
  `request.conflict`.
- A method that a Builder route does not take answers 405
  `request.method-not-allowed` with `Allow` (`builderMethodNotAllowed`).
- No Builder route registers `OPTIONS`. One route, registered first
  (`builderOptionsRequest`, `builderOptions`), answers `OPTIONS` on every
  Builder path that has a route: 204 with `Allow`
  (`builderAllowedMethods`, which adds `OPTIONS`). It runs no handler,
  permission check or service (`TestBuilderOptionsRunNoHandler`). A new
  route registers only its real methods.
- Publish `warnings` and `errors`, and the `warnings` and
  `publishBlockers` of the Topology YAML download, are issues. `readIssues`
  in `api.js` reads them. `publishRefusal` in `publish.js` picks the form
  field by code (`REFUSAL_RULES`). Generation and legacy conversion
  warnings are plain text.
