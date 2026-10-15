# Go Core Instructions

Read root [`AGENTS.md`](../../AGENTS.md) first. These rules apply to all work
under `src/go/`.

## Source Map

- `cmd/`: Cobra commands; `api/`: domain operations; `types/`: versioned
  resources and schemas.
- `app/`: built-in and user app execution; `scheduler/`: VM placement.
- `store/`: BoltDB/etcd abstraction; `web/`: REST, websocket, RBAC, and UI
  serving; `util/mm/`: minimega integration.

## Commands

Run from `src/go/`:

| Purpose | Command |
|---|---|
| Focused tests | `go test -race ./path/to/package/...` |
| All Go tests | `go test -race ./...` or `make test` |
| Non-fixing lint | `make check` |
| Format / fixing lint | `make format` / `make lint` |
| Generate Go outputs | `make generate` |
| Render the API reference (`/docs/`) | `make api-docs` |
| Build Linux binary | `make phenix` |

Use targeted package tests first. `make check` here does not generate files;
the root target does.

## Generation and Compatibility

- Run `make generate` after protobuf, store-interface, or RBAC policy changes.
- Never manually edit `store/mock.go`, `web/proto/*.pb.go`,
  `web/rbac/known_policy.go`, or `web/public/docs/index.html`. They are
  gitignored build outputs, not part of the change.
- Generators must be deterministic and write only gitignored files: the root
  `make check-generated` (run by `ci.yml`) generates everything twice and fails
  on differing output or on new or changed tracked files. Pin generator tool
  versions; the Redocly CLI that renders `openapi.yml` is pinned only in
  `scripts/api-docs.sh`.
- `openapi.yml` must stay valid OpenAPI: a prek hook runs
  `scripts/api-docs.sh lint web/public/docs/openapi.yml` (from the repository
  root) when it changes.
- Preserve bundled third-party assets under `web/public/` unless explicitly
  upgrading or replacing them.
- Config changes must keep versioned structs, interfaces, YAML/JSON tags,
  OpenAPI schemas, upgrades, examples, and API output aligned.
- Preserve old config upgrades and persisted BoltDB/etcd compatibility.
- RBAC changes require generated policy, migrations when needed, and migration
  tests. Keep CLI, REST, websocket, and UI authorization equivalent.
- API route changes require domain behavior, `web/server.go`, RBAC,
  `web/public/docs/openapi.yml`, consumers, and tests.

## Go Conventions

- Follow `.golangci.yml` and `gofmt`; Go and Makefiles use tabs.
- Return operational and validation errors using existing patterns; do not
  silently recover.
- Use `phenix/util/plog` for core logs with the correct `LogType` and structured
  key/value fields.
- A web handler for a security-relevant action (reading captures or files,
  consoles and remote sessions, changing disks or files) logs it with
  `phenix/web/audit`: `Denied` at each RBAC refusal, `Failure` when the
  authorized operation fails, and `Success` once it works. Add a new action to
  the action table in `docs/content/logging.md`.
- A new cache or reused result must name its bound and invalidation in the
  registry in `util/freshness/doc.go`, and report its age with
  `freshness.SetObserved` when it serves minimega data. A failed minimega read
  is an error or an explicit unknown flag, never an empty success.
- Consult [minimega API docs](https://sandia-minimega.github.io/minimega/reference/minimega/) and
  [source](https://github.com/sandia-minimega/minimega) before changing code
  that constructs commands or depends on minimega behavior.

## API Contract Tests

`web/contract_test.go` drives each API route through the router `phenix ui`
serves: auth modes, RBAC, base path, and error statuses and bodies. A new or
changed route adds a row to `contractRoutes` (with the permissions it needs)
and its error cases to `TestContractErrors`, and goes in
`web/public/docs/openapi.yml`. `TestRouterMatchesOpenAPI` fails for a route
the router serves that `openapi.yml` lacks, or the reverse; leave a route out
of `openapi.yml` only through `undocumentedRoutes`, with the reason.

## CI

`.github/workflows/ci.yml` generates artifacts, runs `make check`, and executes
race-enabled tests; its `generated` job runs `make check-generated` from the
repository root. Go changes can also trigger `.github/workflows/frontend.yml`
because browser smoke tests build and run the backend. Keep workflow paths,
Go/tool versions, and local commands aligned.
