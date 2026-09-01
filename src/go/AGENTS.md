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
| Generate all outputs | `make generate` |
| Build Linux binary | `make phenix` |

Use targeted package tests first. `make check` here does not generate files;
the root target does.

## Generation and Compatibility

- Run `make generate` after protobuf, store-interface, or RBAC policy changes.
- Never manually edit `store/mock.go`, `web/proto/*.pb.go`, or
  `web/rbac/known_policy.go`; include regenerated outputs in the change.
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
- Consult [minimega API docs](https://sandia-minimega.github.io/) and
  [source](https://github.com/sandia-minimega/minimega) before changing code
  that constructs commands or depends on minimega behavior.

## CI

`.github/workflows/ci.yml` generates artifacts, runs `make check`, and executes
race-enabled tests. Go changes can also trigger `.github/workflows/frontend.yml`
because browser smoke tests build and run the backend. Keep workflow paths,
Go/tool versions, and local commands aligned.
