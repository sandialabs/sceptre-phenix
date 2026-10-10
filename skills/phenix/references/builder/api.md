# REST API contracts

Part of the [Builder references](../builder.md). The route index, how the
routes authorize, ETags, error codes and error bodies.

User docs: [REST API](https://phenix.sceptre.dev/latest/builder/administration/#rest-api)
lists each route with its `configs` permission, and
[Permissions](https://phenix.sceptre.dev/latest/builder/administration/#permissions)
describes the RBAC resources and the Builder role.
`src/go/web/public/docs/openapi.yml` (served at `/docs/`) describes every
request and response. `TestBuilderRoutesDocumented` in `web/builder_test.go`
fails when a route or method is not in it.

## Routes

All routes are below `/api/v1`. `web/builder.go` registers them. Each row
names the handler file and the reference that holds the contract.

| Routes | Handler | Contract |
|---|---|---|
| `GET /schemas/builder/v1`, `/schemas/builder/templates/v1`, `/schemas/builder/package/v1` (`schemas` `get` on `builder`) | `builder.go` | [document.md](document.md) |
| `/builder/drafts`, `/builder/drafts/{owner}/{draft}`, `…/snapshots[/{snapshot}]`, `…/cursor` | `builder_drafts.go` | [saving.md](saving.md) |
| `…/shares`, `…/shares/candidates` | `builder_shares.go` | [Shares](#shares) |
| `…/publish` (with `dryRun`) | `builder_publish.go`, `builder_preview.go` | [publishing.md](publishing.md) |
| `…/preflight` | `builder_preflight.go` | [publishing.md](publishing.md#preflight) |
| `/builder/sources`, `/builder/generate`, `/builder/legacy` | `builder_sources.go`, `builder_legacy.go` | [sources.md](sources.md) |
| `/builder/export/topology`, `/builder/package`, `/builder/package/resolve` | `builder_export.go`, `builder_package.go` | [sources.md](sources.md#downloads-and-packages) |
| `/builder/documents[/{document}]`, `/builder/topologies/{topology}/document` | `builder_documents.go` | [published-documents.md](published-documents.md) |
| `/builder/icons[/{icon}]` | `builder_icons.go` | [templates-and-icons.md](templates-and-icons.md#icon-library) |
| `/builder/templates`, `…/candidates`, `/builder/templates/{owner}/items`, `/collections`, `/delete`, `/restore`, `/share`, `/publish` | `builder_templates.go` | [templates-and-icons.md](templates-and-icons.md#template-library) |

## Authorization

- Every Builder request needs the `configs` permission of its verb (`list`,
  `get`, `create`, `update`, `delete`). Builder access never exceeds config
  access. Publish also needs the permissions of each config it writes.
- A caller who cannot see another user's draft (no share, no
  `builder-drafts` permission) gets 404, so the server never discloses that
  a draft exists. A caller who can see it but may not do the operation (a
  viewer who saves, an editor who deletes or shares) gets 403.
- `builder-drafts` resource names are `{owner}/{draft-id}`.
  `filepath.Match` does not match `/`, so a bare `"*"` never matches. Use
  `"*/*"` (as `global-admin` does) or `alice/*`. `create` is not a
  `builder-drafts` verb: a draft is always the caller's own.
- `builder-templates` `publish` and `builder-icons` `update`/`delete` take
  no resource names. They are literal checks
  (`builderTemplatesPublishAllowed`, `builderIconsUpdateAllowed`,
  `builderIconsDeleteAllowed` in `web/builder.go`), so `make generate`
  records them in the known policy.
- A template library belongs to its owner only. No role, also not Global
  Admin, reads or changes another user's library (404). The one exception
  is to take back a server-wide item with `builder-templates` `publish`.
- The built-in role `Builder` is `api/config/default/builder.yml`. Its
  `configs` rule names `Topology/*`, `Scenario/*` and `Experiment/*`, never
  `*/*`: that would expose User and Role configs (password hashes and role
  changes). `rbac.EnsureBuilderRolePermissions` (`web/rbac/migrations.go`,
  called from `web.Init` at every start) creates the role when it is
  missing, and adds the `builder-templates` and `builder-icons` grants that
  an existing role and its users lack. It changes nothing else.

### Shares

`PUT …/shares` replaces the whole list. It needs `If-Match: "shares-N"`,
the share list's own tag. Unknown users, the owner and duplicates get 422
with per-user `errors`. A share binds to the recipient's account, so a user
deleted and created again under the same name loses it. A share never gives
delete or sharing. `…/shares/candidates` lists every account that can
receive a share, whatever the caller's `users` permissions. Changing shares
changes the draft's ETag too.

Draft responses carry `access` (`owner`, `edit`, `view`), `via` (`share`,
`role`) for other users' drafts, `canShare` and `shares` for the owner, and
`canDelete` (computed on every read). They also carry `experiment`: an
experiment the publication made that still exists, read from its
`builder-experiment` annotation, only for a caller with `experiments` `get`
on it. `GET /builder/drafts` also lists `damaged` drafts (metadata this
server cannot read), which can only be deleted with their `etag`.

## ETags

- Every change to an existing draft needs `If-Match` with the quoted ETag
  of the last response. A missing or malformed tag is 400 and a stale one
  412. A 412 carries the current `ETag`.
- Draft responses also carry the tag in the body as `etag`. Clients read
  that one, because a compressing proxy can rewrite the header (`W/"3"`,
  `"3-gzip"`).
- Library templates and collections have strong ETags `"<version>"`. Only
  the two template `PUT` routes need `If-Match`.
- A change whose write succeeded but whose cleanup of old content failed
  answers its normal status, body and new `ETag`, plus a `Warning: 199`
  header that names the operation. The server logs the cause and never
  sends it. A failure would only make the client retry with a stale tag.
- When etcd is out of space, any write it refuses (Builder saves and config
  writes too) answers 507 with `etcd is out of space: ...` as `message`
  (`store.ErrNoSpace`, OpenAPI `InsufficientStorage`). The editor keeps the
  changes queued and retries.

## Error codes

Every Builder problem has a stable code: dotted lowercase words, the first
of which names the subject (`node.hostname.duplicate`,
`publish.topology.exists`, `request.not-found`). The one registry is
`types/builder/codes.go` (`bdoc.Codes()`, `LookupCode`, constants
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
  package or document that does not validate and for what only Publish
  refuses. Other phenix routes have neither
  (`TestNonBuilderErrorsCarryNoCode`).
- Draft answers that a client must tell apart have `draft.*` codes:
  `draft.stale` (412), `draft.shares.stale` (412), `draft.conflict` (409)
  and `draft.snapshot.current` (409). Other routes use `request.stale` and
  `request.conflict`.
- A method a Builder route does not take answers 405
  `request.method-not-allowed` with `Allow` (`builderMethodNotAllowed`).
- Publish `warnings` and `errors`, and the Topology YAML download's
  `warnings` and `publishBlockers`, are issues. `readIssues` in `api.js`
  reads them. `publishRefusal` in `publish.js` picks the form field by code
  (`REFUSAL_RULES`). Generation and legacy conversion warnings are plain
  text.
