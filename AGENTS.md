# AGENTS.md

## Project and Instruction Scope

phēnix is a Go/Cobra and Vue 3 platform for defining, deploying, and managing
cyber experiments on minimega. Major areas are `src/go/` (core, CLI, API, and
server), `src/js/` (web UI), `examples/` (Go and Python apps), and container
assets in `docker/` and `podman/`.

Root rules apply everywhere. Before changing a scoped area, read its closer
instructions:

| Trigger | Required instructions |
|---|---|
| Any Go core, CLI, API, schema, store, RBAC, protobuf, minimega, or server work | [`src/go/AGENTS.md`](src/go/AGENTS.md) |
| Any Vue, JavaScript/TypeScript, Vite, Vitest, UI auth, or Playwright work | [`src/js/AGENTS.md`](src/js/AGENTS.md) |
| Any Go or Python example app work | [`examples/AGENTS.md`](examples/AGENTS.md) |

The closest `AGENTS.md` adds area-specific rules; this file remains binding.

## phēnix Domain Reference

Before answering usage questions or changing CLI commands, REST routes,
configuration resources, experiments, VMs, images, VLANs, settings, apps,
SCORCH, minimega integration, or cyber-range workflows, read
[`skills/phenix/SKILL.md`](skills/phenix/SKILL.md). For `phenix image`, `Image`
configs, build scripts, overlays, or vmdb2 work, also read
[`skills/phenix-image/SKILL.md`](skills/phenix-image/SKILL.md). Use code as final
authority when guidance differs, and update the skills when behavior changes.

`SKILL.md` stays broad and always loaded; deep, area-specific material lives in
`skills/phenix/references/` and is read only when that area is in scope. Put
new detail in the matching reference file rather than growing `SKILL.md`, and
leave a one-line pointer to it from `SKILL.md`.

## Architecture

- `src/go/main.go` starts commands from `src/go/cmd/`.
- `src/go/api/` implements domain operations; `src/go/types/` owns versioned
  config models and schemas.
- `src/go/app/` runs apps; `scheduler/` assigns VMs; `store/` abstracts the
  BoltDB store; `web/` serves REST, websockets, RBAC, and the built UI.
- `src/js/src/views/` contains pages, `components/` reusable UI, `api/` the
  REST requests by resource, and `utils/` shared helpers. `router.js`,
  `store.js`, and `main.js` wire the app.
- Root builds copy `src/js/dist/` into `src/go/web/public/`; the Vite
  development proxy is described in [`src/js/AGENTS.md`](src/js/AGENTS.md).
- The web UI targets desktop browsers only; phēnix is not used on phones or
  other mobile devices, so mobile support is out of scope.
- `src/go/web/public/` holds what `//go:embed all:public` in
  `src/go/web/assets.go` embeds: the copied UI build and the bundled noVNC
  and xterm.js. It is excluded from every formatting and lint hook in
  `prek.toml`, so `make check` will not catch anything under it, and a
  change there only appears after the Go binary is rebuilt.

## Essential Runtime Contracts

- phēnix apps receive experiment JSON on stdin and one lifecycle argument:
  `configure`, `pre-start`, `post-start`, `running`, or `cleanup`.
- Apps must emit only valid experiment JSON on stdout and single-line structured
  JSON logs on stderr. Python apps use `phenix_apps.common.logger`; Go apps use
  `log/slog` with a JSON stderr handler.
- Core logging uses `phenix/util/plog`: call `plog.Debug`, `Info`, `Warn`, or
  `Error` with the appropriate `LogType`, message, and structured key/value
  fields. The core routes records to console, system log, and web UI.
- API auth uses `X-Phenix-Auth-Token`, not `Authorization`.
- Runtime precedence is CLI flags, environment, config file, then defaults.
- Never delete a live `config.yaml`; use `phenix settings unset` so its watcher
  remains active.

## Setup and Common Commands

Requirements: Go 1.24+, Node.js 24+, Python 3.12+, `protoc` 3.12+, npm, and
Docker for full deployments and image builds.

| Purpose | Command |
|---|---|
| Inspect targets | `make help` or `make help-all` |
| Install development tools | `make install-dev` |
| Install with another Python | `SYSTEM_PYTHON=/path/to/python3.12 make install-dev` |
| Generate mocks, protobufs, RBAC, and the API reference | `make generate` (Go only: `make generate-go`) |
| Render / lint the API reference (`/docs/`) | `make api-docs` / `scripts/api-docs.sh lint src/go/web/public/docs/openapi.yml` |
| Lint the config schemas | `scripts/api-docs.sh lint-schemas` |
| Format / fixing lint for Go/examples | `make format` / `make lint` |
| Generate, then non-fixing lint | `make check` |
| Regenerate the CLI reference (`docs/content/cli/`, tracked) | `make cli-docs` |
| Check generated outputs are reproducible and the CLI reference is current | `make check-generated` |
| Go core and example tests | `make test` |
| Build UI, API reference, and `bin/phenix` | `make build` (`SKIP_UI=1`, `SKIP_API_DOCS=1` reuse or skip them) |
| Build UI only | `make ui` |
| Build container / Debian package | `make docker` / `make deb` |
| Build / preview docs site | `make docs-build` / `make docs-serve` |

Use `npm ci`, never dependency-updating installs, for locked frontend
dependencies. The root `make check` runs generation and may modify generated
files. The root `make test` excludes Vitest and Playwright.

Docker is the normal deployment. For automation, omit TTY flags:

```bash
docker exec phenix phenix experiment list
```

## Change Impact and Compatibility

When changing a capability, inspect every applicable surface:

| Change | Also inspect or update |
|---|---|
| CLI, REST, or UI capability | Implement parity in the other two when applicable and feasible; document gaps |
| CLI command, flag, or help text | Regenerate the CLI reference with `make cli-docs` and commit `docs/content/cli/` and `docs/mkdocs.yml` |
| Config field or version | Versioned structs, interfaces, schemas, examples, API output, skill, and docs |
| REST route | Domain API, route registration in `src/go/web/server.go`, RBAC, OpenAPI, UI client, and tests |
| Store interface | Implementations, mock generation, callers, and tests |
| Protobuf | `.proto`, generated files, consumers, and tests |
| RBAC role or policy | Policy generation, migrations, authorization surfaces, and migration tests |
| minimega command | minimega API/source behavior and focused tests |
| Log or error message text | Docs and skill text that quotes it, such as the Troubleshooting sections in `docs/content/` and `skills/phenix/references/` |
| New feature or significant behavior change | A `phenix doctor` check when it applies: a new setting, external program, file, directory, service, port or feature flag (see [`src/go/AGENTS.md`](src/go/AGENTS.md)) |

Preserve persisted BoltDB data, RBAC migrations, and public API compatibility
unless a breaking change is deliberate and documented. phēnix has no config
upgraders: each kind decodes only at its stored version, so changing that
version breaks existing configs.
Keep CLI, REST, websocket, and UI authorization consistent.

## Validation

Run the narrowest existing test, build, or lint command covering the change,
then the broader affected-area check from the scoped instructions. Add focused
tests for behavior changes. Report checks you cannot run; do not silently skip
requirements such as Docker, minimega, VM images, root privileges, or a running
backend. Review generated diffs before submission.

Services started for local testing (`phenix ui`, Vite, Playwright targets,
scratch servers) must not listen on ports 9200, 5601, 3000, 3001, or 9001
unless a hardcoded value requires it or the user asks; choose another free port
and pass it explicitly, for example
`phenix ui --listen-endpoint 127.0.0.1:3080` with
`E2E_BASE_URL=http://127.0.0.1:3080`. The Playwright and Lighthouse configs and
`scripts/ci-start-phenix-ui.sh` default to port 3197.

## Documentation and References

| Need | Reference |
|---|---|
| Build, install, logging, config | `README.md` |
| App development | `examples/README.md`, `examples/TUTORIAL.md` |
| Config interfaces and fields | `src/go/types/interfaces/`, `src/go/types/version/` |
| YAML config schemas | `src/go/types/schemas/config.yaml` (the envelope every config matches), `src/go/types/version/schemas/{v0,v1,v2}.yaml` (each kind's spec), one file per `apiVersion` holding the kinds stored at it: `v0` `Workflow`, `v1` `Topology`/`Experiment`/`Image`/`User`/`Role`, `v2` `Scenario` |
| CLI and REST implementation | `src/go/cmd/`, `src/go/web/server.go` |
| Narrative docs source (phenix.sceptre.dev) | [`docs/`](docs/) |
| Internet-hosted narrative docs | [phenix.sceptre.dev](https://phenix.sceptre.dev/latest/) |
| minimega commands and behavior | [API docs](https://sandia-minimega.github.io/minimega/reference/minimega/), [source](https://github.com/sandia-minimega/minimega) |
| Official apps and SCORCH components | [`sceptre-phenix-apps`](https://github.com/sandialabs/sceptre-phenix-apps) |
| Image configs, overlays, scripts | [`sceptre-phenix-images`](https://github.com/sandialabs/sceptre-phenix-images) |
| Reusable topologies | [`sceptre-phenix-topologies`](https://github.com/sandialabs/sceptre-phenix-topologies) |

Consult minimega documentation and source whenever code constructs commands or
interacts with minimega. Use companion repositories when local code lacks app,
image, documentation, or topology details.

## Change Management

- Follow `.github/CONTRIBUTING.md`; use Conventional Commit messages.
- The project is English-only (US dialect): all contributions, including code,
  comments, documentation, commit messages, and pull requests, are written in
  English, and every `<html>` tag must include `lang="en-US"`.
- Branch names use a Conventional Commit type and no `/`, for example
  `feat-add-user-authentication`.
- Use rebase workflow and one commit per logical feature; PRs normally contain
  one commit. Squash work-in-progress commits and update the message.
- Update `CHANGELOG.md` for code or behavior changes and `CODEOWNERS` for new
  areas or ownership changes.
- New user-facing features need a minimal README example and documentation in
  [`docs/`](docs/).
- Keep API docs, schemas, examples, and the phēnix skill (`SKILL.md` and its
  `references/`) aligned with behavior.
- Use issue and PR templates under `.github/`. Keep PR descriptions concise:
  purpose, relevant changes, related issue, and reviewer context only.

## CI and Release Safety

Most GitHub Actions workflows are path-scoped:

- `ci.yml` generates code and tests Go with a coverage summary; its
  `generated` job runs `make check-generated`, which also fails on a stale CLI
  reference.
- `frontend.yml` runs Vitest with a coverage summary and, for changes that can
  affect the browser (the paths its `changes` job lists), builds the UI and
  backend once per `VITE_AUTH` mode and shares those builds with the Playwright
  jobs, which run in parallel in the pinned Playwright image, and with the UI
  bundle budget and Lighthouse jobs, the latter writing a budget summary with
  deltas against the default branch.
- Both coverage summaries warn, without failing, below
  `.github/coverage-minimums.json`.
- `examples.yml` checks the Go and Python examples.
- `docs.yml` builds the docs and API reference and publishes them from `main`.
  Every API reference build, local, container or CI, goes through
  `scripts/api-docs.sh`, which pins the Redocly CLI version; prek hooks lint
  `openapi.yml` and the config schemas with it.
- `lint.yml` runs the `prek` hooks on every push, including golangci-lint and
  deadcode for Go (`src/go/prek.toml`) and knip for the UI
  (`src/js/prek.toml`).
- `packages.yml` builds the Docker, Debian and Podman outputs on every push
  that changes more than the docs, scans each image it builds with grype, and
  makes SPDX SBOMs of both images and the `.deb` with syft, attaching them to a
  release and keeping them as an artifact otherwise.
- `stress.yml` repeats the concurrency-heavy Go packages under `-race` weekly,
  and `fuzz.yml` runs every Go fuzz target for a bounded time weekly.
- `security.yml` reports govulncheck and `npm audit` findings on pull requests
  and weekly, and grype findings for the published image weekly. It and
  `packages.yml` write the findings to the job summary through
  `scripts/vuln-report.sh` (`go`, `npm` or `grype`, also runnable locally) and
  do not fail the run.
- `cancel-closed-pr.yml` cancels a closed pull request branch's push-triggered
  runs (branches in this repository only), while each workflow's own
  concurrency group cancels the pull request's `pull_request` runs. Those
  groups are keyed by pull request number, not ref, so a closed pull request's
  run cannot cancel runs on the base branch.

Update affected path filters, inputs, generated artifacts, tool versions, and
local-equivalent commands together. Preserve least-privilege permissions,
supported action versions, lockfile caches, and shared version values.

Do not publish artifacts, push images, or create releases under any
circumstances. The only exception is packages that a fork's own workflows
publish to that fork's registry when you push a branch to the fork (for example
`ghcr.io/<fork-owner>/sceptre-phenix/phenix:<branch>`); pushing branches to a
fork is allowed.
