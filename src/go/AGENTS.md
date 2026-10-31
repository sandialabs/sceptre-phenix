# Go Core Instructions

Read root [`AGENTS.md`](../../AGENTS.md) first. These rules apply to all work
under `src/go/`.

## Source Map

- `cmd/`: Cobra commands; `api/`: domain operations; `types/`: versioned
  resources and schemas.
- `app/`: built-in and user app execution; `scheduler/`: VM placement.
- `store/`: BoltDB store abstraction; `web/`: REST, websocket, RBAC, and UI
  serving; `util/mm/`: minimega integration.

## Commands

Run from `src/go/`:

| Purpose | Command |
|---|---|
| Focused tests | `go test -race ./path/to/package/...` |
| All Go tests | `go test -race ./...` or `make test` |
| Go tests with coverage | `make test-cover` |
| Non-fixing lint | `make check` |
| Format / fixing lint | `make format` / `make lint` |
| Dead code check | `make deadcode` |
| Code that only tests reach (advisory) | `make deadcode-tests` |
| Generate all outputs | `make generate` |
| Build Linux binary | `make phenix` |

Use targeted package tests first. `make check` here does not generate files;
the root target does.

## Generation and Compatibility

- Run `make generate` after protobuf, store-interface, or RBAC policy changes.
- Before you change Builder code (`api/builder/`, `types/builder/`,
  `web/builder*.go`) or `types/version/schemas/`, read
  `../../skills/phenix/references/builder.md`
  and the reference that its routing table names. These changes need
  `make generate`, because CI checks the committed schema bundle.
- Never manually edit `store/mock.go`, `web/proto/*.pb.go`, or
  `web/rbac/known_policy.go`; include regenerated outputs in the change.
- Preserve bundled third-party assets under `web/public/` unless explicitly
  upgrading or replacing them.
- Config changes must keep versioned structs, interfaces, YAML/JSON tags,
  OpenAPI schemas, examples, and API output aligned.
- Preserve persisted BoltDB compatibility. A kind decodes only at its version
  in `types/version.StoredVersion`; there are no config upgraders.
- A kind's schema lives only in `types/version/schemas/<version>.yaml` for its
  stored version. Config validation and the UI config editor both read the
  file of the config's own `apiVersion`, so a schema change goes in that file.
- RBAC changes require generated policy, migrations when needed, and migration
  tests. Keep CLI, REST, websocket, and UI authorization equivalent.
- API route changes require domain behavior, `web/server.go`, RBAC,
  `web/public/docs/openapi.yml`, consumers, and tests.

## Go Conventions

- Follow `.golangci.yml` and `gofmt`; Go and Makefiles use tabs.
- Return operational and validation errors using existing patterns; do not
  silently recover.
- Remove dead code. `make deadcode` fails on a function that nothing reaches,
  not even a test. Add a function to `.deadcode-allow` only when code outside
  this module or reflection uses it. Keep the list short. Put a reason above
  each entry. The Makefile pins deadcode to x/tools v0.42.0, the newest
  release that builds with Go 1.24. Do not raise the pin above the Go version
  in `go.mod`.
- Use `phenix/util/plog` for core logs with the correct `LogType` and structured
  key/value fields.
- Consult [minimega API docs](https://sandia-minimega.github.io/minimega/reference/minimega/) and
  [source](https://github.com/sandia-minimega/minimega) before changing code
  that constructs commands or depends on minimega behavior.

## Test Style

Follow the Go wiki [TestComments](https://go.dev/wiki/TestComments) in Go tests:

- Do not use assert libraries, and do not write helpers that only wrap a
  comparison and a fail. A check helper returns an error or a diff for the
  test to report.
- Use `t.Error` for independent checks. Use `t.Fatal` only when the test cannot
  continue, or inside a `t.Run` subtest. Run table cases as `t.Run` subtests.
- Compare whole values with `cmp.Diff` from `github.com/google/go-cmp` and
  print the result as `(-want +got)`. Compare decoded data, not marshaled JSON
  or YAML.
- Check errors with `errors.Is` or `errors.As`, not with error text.
- Print the actual value before the expected one:
  `Func(input) = got, want want`.
- Call `t.Helper` in every test helper.

## CI

`.github/workflows/ci.yml` generates artifacts, runs `make check`, executes
race-enabled tests, and runs `make deadcode`. Go changes can also trigger
`.github/workflows/frontend.yml` because browser smoke tests build and run the
backend. Keep workflow paths, Go/tool versions, and local commands aligned.
