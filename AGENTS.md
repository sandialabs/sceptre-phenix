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
[`skills/phenix/SKILL.md`](skills/phenix/SKILL.md). Use code as final authority
when guidance differs, and update the skill when behavior changes.

## Architecture

- `src/go/main.go` starts commands from `src/go/cmd/`.
- `src/go/api/` implements domain operations; `src/go/types/` owns versioned
  config models and schemas.
- `src/go/app/` runs apps; `scheduler/` assigns VMs; `store/` abstracts BoltDB
  and etcd; `web/` serves REST, websockets, RBAC, and the built UI.
- `src/js/src/views/` contains pages, `components/` reusable UI, and `utils/`
  shared helpers. `router.js`, `store.js`, and `main.js` wire the app.
- The Vite server proxies `/api/v1`, `/version`, and `/features` to
  `localhost:3000`. Root builds copy `src/js/dist/` into `src/go/web/public/`.

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
- Runtime precedence is CLI flags, config file, environment, then defaults.
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
| Generate mocks, protobufs, and RBAC | `make generate` |
| Format / fixing lint for Go/examples | `make format` / `make lint` |
| Generate, then non-fixing lint | `make check` |
| Go core and example tests | `make test` |
| Build UI and `bin/phenix` | `make build` |
| Build UI only | `make ui` |
| Build container / Debian package | `make docker` / `make deb` |

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
| Config field or version | Versioned structs, interfaces, schemas, examples, API output, skill, and docs |
| REST route | Domain API, route table, RBAC, OpenAPI, UI client, and tests |
| Store interface | Implementations, mock generation, callers, and tests |
| Protobuf | `.proto`, generated files, consumers, and tests |
| RBAC role or policy | Policy generation, migrations, authorization surfaces, and migration tests |
| minimega command | minimega API/source behavior and focused tests |

Preserve v1/v2 config upgrades, persisted BoltDB/etcd data, RBAC migrations, and
public API compatibility unless a breaking change is deliberate and documented.
Keep CLI, REST, websocket, and UI authorization consistent.

## Validation

Run the narrowest existing test, build, or lint command covering the change,
then the broader affected-area check from the scoped instructions. Add focused
tests for behavior changes. Report checks you cannot run; do not silently skip
requirements such as Docker, minimega, VM images, root privileges, or a running
backend. Review generated diffs before submission.

## Documentation and References

| Need | Reference |
|---|---|
| Build, install, logging, config | `README.md` |
| App development | `examples/README.md`, `examples/TUTORIAL.md` |
| Config interfaces and fields | `src/go/types/interfaces/`, `src/go/types/version/` |
| YAML config schemas | `src/go/types/version/schemas/{v0,v1,v2}.yaml` |
| CLI and REST implementation | `src/go/cmd/`, `src/go/web/server.go` |
| Internet-hosted narrative docs | [phenix.sceptre.dev](https://phenix.sceptre.dev/latest/) |
| minimega commands and behavior | [API docs](https://sandia-minimega.github.io/), [source](https://github.com/sandia-minimega/minimega) |
| Official apps and SCORCH components | [`sceptre-phenix-apps`](https://github.com/sandialabs/sceptre-phenix-apps) |
| Image configs, overlays, scripts | [`sceptre-phenix-images`](https://github.com/sandialabs/sceptre-phenix-images) |
| Source for phenix.sceptre.dev | [`sceptre-phenix-docs`](https://github.com/sandialabs/sceptre-phenix-docs) |
| Reusable topologies | [`sceptre-phenix-topologies`](https://github.com/sandialabs/sceptre-phenix-topologies) |

Consult minimega documentation and source whenever code constructs commands or
interacts with minimega. Use companion repositories when local code lacks app,
image, documentation, or topology details.

## Change Management

- Follow `.github/CONTRIBUTING.md`; use Conventional Commit messages.
- Branch names use a Conventional Commit type and no `/`, for example
  `feat-add-user-authentication`.
- Use rebase workflow and one commit per logical feature; PRs normally contain
  one commit. Squash work-in-progress commits and update the message.
- Update `CHANGELOG.md` for code or behavior changes and `CODEOWNERS` for new
  areas or ownership changes.
- New user-facing features need a minimal README example and documentation in
  [`sceptre-phenix-docs`](https://github.com/sandialabs/sceptre-phenix-docs).
  Existing changes may also require docs; cross-link both pull requests.
- Keep API docs, schemas, examples, and the phēnix skill aligned with behavior.
- Use issue and PR templates under `.github/`. Keep PR descriptions concise:
  purpose, relevant changes, related issue, and reviewer context only.

## CI and Release Safety

GitHub Actions is path-scoped: `ci.yml` generates, lints, and tests Go;
`frontend.yml` runs Vitest, builds UI/backend, and runs Playwright smoke tests;
`examples.yml` checks Go/Python examples; `packages.yml` builds Docker, Debian,
and Podman outputs. Update affected path filters, inputs, generated artifacts,
tool versions, and local-equivalent commands together. Preserve least-privilege
permissions, supported action versions, lockfile caches, and shared version
values.

Do not publish artifacts, push images, or create releases under any
circumstances.
