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
| Non-fixing lint | `make check` |
| Format / fixing lint | `make format` / `make lint` |
| Generate Go outputs | `make generate` |
| Render the API reference (`/docs/`) | `make api-docs` |
| Regenerate the tracked CLI reference (`docs/content/cli/`) | `make cli-docs` |
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
- A long-lived web handler (websocket, terminal, streamed response) registers
  with `phenix/web/lifecycle` (`Track`, `TrackConn` or `WithStop`) and ends on
  its stop signal, a gorilla websocket with close code 1001 and
  `lifecycle.CloseReason`, so `phenix ui` shuts down within its grace period.
- Remove a function nothing reaches from `main` or a test; the `go-deadcode`
  prek hook (`scripts/go-deadcode.sh`) fails on one. Keep one on purpose only
  by listing it, with the reason, in `deadcode-allow.txt`.
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

## CI

`.github/workflows/ci.yml` generates artifacts, runs `make check`, and executes
race-enabled tests in shuffled order, uploading a coverage profile and writing
a coverage summary; see the root `AGENTS.md` CI list for its minimums and the
`generated` job. `.github/workflows/stress.yml` runs `make test-stress` weekly
and on demand, and `.github/workflows/fuzz.yml` runs `make fuzz` weekly,
uploading any failing input. `.github/workflows/security.yml` reports
govulncheck findings in the job summary without failing (locally,
`scripts/vuln-report.sh go` with `govulncheck` installed). Go changes can also trigger `.github/workflows/frontend.yml`
because browser smoke tests build and run the backend. Keep workflow paths,
Go/tool versions, and local commands aligned.
