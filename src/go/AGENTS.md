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
| All Go tests | `make test` (shuffled, `-race`; `V=1` for verbose output, `COVERPROFILE=<file>` for coverage) |
| Go tests with coverage | `make test-cover` (`make test COVERPROFILE=coverage.out`, then prints the total) |
| Repeat the concurrency-heavy packages | `make test-stress` (`STRESS_COUNT`, default 20) |
| Fuzz every fuzz target | `make fuzz` (`FUZZTIME` each, default 30s) |
| Unreachable functions | `make deadcode` (also a prek hook) |
| Code that only tests reach (advisory) | `make deadcode-tests` |
| Non-fixing lint | `make check` |
| Format / fixing lint | `make format` / `make lint` |
| Generate all outputs | `make generate` |
| Render the API reference (`/docs/`) | `make api-docs` |
| Regenerate the tracked CLI reference (`docs/content/cli/`) | `make cli-docs` |
| Build Linux binary | `make phenix` |

Use targeted package tests first. `make check` here does not generate files;
the root target does.

## Generation and Compatibility

- Run `make generate` after protobuf, store-interface, RBAC policy, or
  embedded OpenAPI schema (`types/schemas/`, `types/version/schemas/`,
  `types/schema.go`) changes.
- Before you change Builder code (`api/builder/`, `types/builder/`,
  `web/builder*.go`) or `types/version/schemas/`, read
  the [Builder](../../AGENTS.md#builder) section of the root `AGENTS.md`.
  These changes need
  `make generate`, because CI checks the committed schema bundle.
- Never manually edit `store/mock.go`, `web/proto/*.pb.go`,
  `web/rbac/known_policy.go`, or `web/public/docs/index.html`. They are
  gitignored build outputs, not part of the change.
- Never manually edit the repository's `schemas/*.schema.json`. They are
  tracked outputs of `make generate-schemas`: include the regenerated files in
  the change.
- Generators must be deterministic and write only gitignored files, except
  `schemas/`: the root `make check-generated` (run by `ci.yml`) generates
  everything twice and fails on differing output or on new or changed tracked
  files. Pin generator tool versions; the Redocly CLI that renders
  `openapi.yml` is pinned only in `scripts/api-docs.sh`.
- `openapi.yml` must stay valid OpenAPI: a prek hook runs
  `scripts/api-docs.sh lint web/public/docs/openapi.yml` (from the repository
  root) when it changes.
- `web/public/` has no third-party assets of its own: the UI build brings
  them (noVNC is an npm dependency of `src/js`), and only
  `web/public/docs/openapi.yml` is tracked.
- Config changes must keep versioned structs, interfaces, YAML/JSON tags,
  OpenAPI schemas, examples, and API output aligned.
- Config schemas are OpenAPI YAML files embedded with `//go:embed`:
  `types/schemas/config.yaml` for the envelope (apiVersion, kind, metadata,
  spec) and `types/version/schemas/v*.yaml` for each kind's spec. Edit the
  YAML, never a Go string. A prek hook lints them with
  `scripts/api-docs.sh lint-schemas` (rules in `types/redocly.yaml`).
  `types/docs_examples_test.go` validates every complete YAML config example
  in `docs/content/`, `README.md` and `skills/` against them; an example that
  elides content with `...` is skipped.
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
- Remove dead code. `make deadcode` and the `go-deadcode` prek hook
  (`scripts/go-deadcode.sh`) fail on a function that nothing reaches from
  `main` or a test. Add a function to `deadcode-allow.txt` only when code
  outside this module or reflection uses it. Keep the list short. Put the
  reason with each entry. `scripts/go-deadcode.sh` pins the deadcode version
  for `make deadcode` and `make deadcode-tests`. Do not raise the pin above
  the Go version in `go.mod`.
- Use `phenix/util/plog` for core logs with the correct `LogType` and structured
  key/value fields.
- A web handler for a security-relevant action (signing in or out, changing
  users, configs, experiments, settings or VM settings, applying workflows, VM
  power, reading captures or files, consoles and remote sessions, changing
  disks or files) logs it with
  `phenix/web/audit`: `Denied` at each RBAC refusal, `Failure` when the
  authorized operation fails, and `Success` once it works. Never put a
  password or token in an audit field. `TestActionsMatchDocs` fails until a
  new action is in the Audited Actions table of `docs/content/logging.md`.
- A new or changed command, flag or help text regenerates the CLI reference
  with `make cli-docs` (root or `src/go`) and commits it: `cmd/gendocs`
  writes `docs/content/cli/` and its nav in `docs/mkdocs.yml` from the Cobra
  tree, and `make check-generated` and the `cli-docs` prek hook fail when
  they are out of date. A flag whose default depends on the user or host
  sets `DocDefaultAnnotation` (`cmd/root.go`) so the output stays the same
  on every machine.
- A new or changed root flag or `ui` setting also updates the settings tables
  in `docs/content/settings.md` and `README.md`. `cmd/settings_docs_test.go`
  fails until the Global Flags table lists every root flag, and until each
  Settings Reference row's environment variable and default match its flag.
  It does not notice a setting missing from a Settings Reference table.
- Keep `phenix doctor` current. When a change adds a feature or significantly
  changes behavior, add or update a doctor check for it when relevant: a new
  setting, external program, file or directory, service or port, or feature
  flag. Register the check in `cmd/doctor_registry.go` with an id, a group
  and a hint. Add its row to the doctor table in `docs/content/settings.md`
  (`TestDoctorChecksMatchDocs` fails without it). Also update the `--skip`
  list and flags in `skills/phenix/references/cli.md`, and run
  `make cli-docs` when the help text changes. A check warns unless the
  problem stops phenix. A check that cannot get its data gives `skip` with
  the reason, and never blocks on minimega, the server or the store lock.
- A new cache or reused result must name its bound and invalidation in the
  registry in `util/freshness/doc.go`, and report its age with
  `freshness.SetObserved` when it serves minimega data. A failed minimega read
  is an error or an explicit unknown flag, never an empty success.
- Code serving a web request, websocket or background job passes its
  context to minimega, so a command still waiting for the shared connection
  gives up once the client leaves or the job is canceled. CLI commands,
  startup and background loops keep plain `mmcli.Run`. The `util/mm` package
  documentation lists the helpers to use.
- `mmcli.RunReadOnly` takes only a command that changes nothing in minimega,
  with its wait line (`HeadRead`, `MeshRead` or `ScreenshotRead`) by the rule
  in its godoc. The pool does not check that the command only reads or that
  the line fits it: reviewers do.
- A long-lived web handler (websocket, terminal, streamed response) registers
  with `phenix/web/lifecycle` (`Track`, `TrackConn` or `WithStop`) and ends on
  its stop signal, a gorilla websocket with close code 1001 and
  `lifecycle.CloseReason`, so `phenix ui` shuts down within its grace period.
- A test that needs a command or other part of its environment calls
  `phenix/util/testenv` (`RequireCommands`, `Skipf`) rather than `t.Skip`:
  it skips locally but fails in CI, where `CI` is set and everything is
  installed. Install a new tool in `ci.yml` along with it.
- Check parsers, sanitizers, round trips and list invariants with
  `pgregory.net/rapid` properties beside the table tests. Keep each property
  fast, never pin a seed, and name generators after their test file.
- Give a parser or sanitizer of untrusted input (request headers, paths,
  archive entry names) a native fuzz target, `func FuzzXxx` in a
  `<file>_fuzz_test.go`, seeded from its table tests and checked against an
  oracle such as `net/http` or a model. `make fuzz` finds every target.
- Consult [minimega API docs](https://sandia-minimega.github.io/minimega/reference/minimega/) and
  [source](https://github.com/sandia-minimega/minimega) before changing code
  that constructs commands or depends on minimega behavior.

## API Contract Tests

`web/contract_test.go` drives each API route through the router `phenix ui`
serves: auth modes, RBAC, base path, and error statuses and bodies. A new or
changed route adds a row to `contractRoutes` (with the permissions it needs)
and its error cases to `TestContractErrors`, and goes in
`web/public/docs/openapi.yml`. `TestRouterMatchesOpenAPI` fails for a route
the router serves that `openapi.yml` lacks, or the reverse: every route that
names its methods is documented, websockets (as a `GET` answering `101`, with
the message protocol in its description), UI pages and routes outside
`/api/v1` included. Only a route of any method, which serves the UI's own
files, stays out, through `routesOfAnyMethod` with the reason.
The contract tests pass each JSON response to `wantDocumentedBody`, which
fails unless the body matches its status's schema in `openapi.yml` and sets
no field the schema leaves out; send `[]`, not `null`, for an empty list.
`TestEveryRouteChecksPermissions` (`web/contract_rbac_test.go`) calls every
route the router serves as a role of no permissions and fails on any answer
but 401 or 403 (404 for the job routes in `rbacHidden`); a route outside
`/api/v1`, or one any signed-in user may call, goes in `rbacExempt` with the
reason.
`types/openapi_test.go` requires every schema and property in `openapi.yml`
to have a title and a description, and every scalar property an example,
default or enum. A route the Unix socket serves gets a case in
`TestContractSocket`.

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
race-enabled tests in shuffled order, uploading a coverage profile and writing
a coverage summary, runs `make deadcode`, and checks the Go example in
`examples/go/`; see the root `AGENTS.md` CI list for its minimums and the
`generated` job. `.github/workflows/stress.yml` runs `make test-stress` weekly
and on demand, and `.github/workflows/fuzz.yml` runs `make fuzz` weekly,
uploading any failing input. `.github/workflows/security.yml` reports
govulncheck findings in the job summary without failing (locally,
`scripts/vuln-report.sh go` with `govulncheck` installed). Go changes can also
trigger `.github/workflows/e2e.yml` because browser smoke tests build and run
the backend. Keep workflow paths, Go/tool versions, and local commands aligned.
