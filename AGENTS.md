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
[`skills/phenix-image/SKILL.md`](skills/phenix-image/SKILL.md).
For Builder code, read [Builder](#builder) below.
Use code as final authority when guidance differs, and update the skills when
behavior changes.

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
- `src/js/src/views/` contains pages, `components/` reusable UI, and `utils/`
  shared helpers. `router.js`, `store.js`, and `main.js` wire the app.
- The Vite server proxies `/api/v1`, `/version`, and `/features` to
  `localhost:3000`. Root builds copy `src/js/dist/` into `src/go/web/public/`,
  with `builder-assets.json` and the `.br` and `.gz` copies of Builder files.
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
| Generate mocks, protobufs, RBAC, and JSON Schemas | `make generate` |
| Format / fixing lint for Go/examples | `make format` / `make lint` |
| Generate, then non-fixing lint | `make check` |
| Find dead code (Go deadcode and knip) | `make deadcode` |
| Go core and example tests | `make test` |
| Build UI and `bin/phenix` | `make build` |
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
| Config field or version | Versioned structs, interfaces, schemas, examples, API output, skill, and docs |
| REST route | Domain API, route table, RBAC, OpenAPI, UI client, and tests |
| Store interface | Implementations, mock generation, callers, and tests |
| Protobuf | `.proto`, generated files, consumers, and tests |
| RBAC role or policy | Policy generation, migrations, authorization surfaces, and migration tests |
| minimega command | minimega API/source behavior and focused tests |
| Log or error message text | Docs and skill text that quotes it, such as the Troubleshooting sections in `docs/content/` and `skills/phenix/references/` |
| Builder behavior | Each code layer in [Builder](#builder), the `builder-*` e2e specs, and the user docs page for the area |

Preserve persisted BoltDB data, RBAC migrations, and public API compatibility
unless a breaking change is deliberate and documented. phēnix has no config
upgraders: each kind decodes only at its stored version, so changing that
version breaks existing configs.
Keep CLI, REST, websocket, and UI authorization consistent.

## Builder

The Builder is the web topology editor at `/builder`, built on Vue Flow. It
edits a `builder/v1` document and publishes it as Topology and Experiment
configs. There is no `phenix builder` command, and the unix socket of
`phenix ui` does not serve the Builder routes.

The user docs in `docs/content/builder/` hold the behavior and UI rules.
Read the page for the area before a change, and update it in the same PR.

### Architecture

Go paths are relative to `src/go/`, and JS paths to `src/js/src/`.

| Layer | Code |
|---|---|
| Browser | `views/Builder.vue`, `components/builder/`, `builder/`. A Pinia store (`builder/store.js`) wraps a pure document model (`builder/model.js`). `builder/autosave.js` saves drafts, and `builder/publish.js` publishes them |
| REST | `web/builder*.go`. `web/builder.go` registers the routes and does the authorization |
| Storage | `api/builder/`: drafts, snapshots, published documents, preflight, and the icon, template, and layer set libraries, in BoltDB through `store.RecordStore`. Each draft change is a compare-and-swap on a revision. This package does no authorization |
| Document | `types/builder/`: the `builder/v1` document, its validation, generation from configs, legacy (`builder-xml`) conversion, packages, and the Topology that Publish writes |

A published Topology has a `builder-doc` annotation (`digest`, `id`, and
`path`) that names its document. Config create and update refuse a Topology
whose `builder-doc` is not valid, even with `--skip-validation`.

### REST API

Routes are below `/api/v1`. Each route needs the RBAC permission `builder`
`use`. Import, Publish, and published documents also need the `configs` verbs
of the configs they read or write. Request bodies, ETag rules, and the
permission of each route are in
`docs/content/builder/details/administration.md#rest-api`.

| Area | Routes |
|---|---|
| Drafts | `GET`/`POST /builder/drafts`, `GET`/`DELETE /builder/drafts/{draft}`, `PATCH /builder/drafts/{draft}/cursor` |
| Snapshots | `GET`/`POST /builder/drafts/{draft}/snapshots`, `GET`/`DELETE /builder/drafts/{draft}/snapshots/{snapshot}` |
| Publishing | `POST /builder/drafts/{draft}/publish`, `POST /builder/drafts/{draft}/preflight` |
| Sources | `GET /builder/sources`, `POST /builder/generate`, `/builder/legacy`, `/builder/export/topology`, `/builder/package`, `/builder/package/resolve` |
| Published | `GET /builder/documents`, `GET`/`DELETE /builder/documents/{document}`, `GET /builder/topologies/{topology}/document` |
| Icons | `GET`/`POST /builder/icons`, `GET`/`PUT`/`DELETE /builder/icons/{icon}` |
| Templates | `GET /builder/templates`, `POST /builder/templates/items`, `PUT /builder/templates/items/{template}`, `POST /builder/templates/collections`, `PUT /builder/templates/collections/{collection}`, `POST /builder/templates/delete` |
| Layer sets | `GET`/`POST /builder/layersets`, `GET`/`PUT /builder/layersets/default`, `GET`/`PUT`/`DELETE /builder/layersets/{layerset}` |
| Storage | `POST /builder/cleanup` |
| Schemas | `GET /schemas/builder/v1`, `/schemas/builder/templates/v1`, `/schemas/builder/package/v1`, `/schemas/builder/layersets/v1` |

### Rules

- After a change to `types/builder/` or `types/version/schemas/`, run
  `make generate` in `src/go`. It writes `src/js/src/builder/schema/*.json`
  and the code table in `docs/content/builder/reference.md`, but writes the
  table only when `docs/` exists. Do not edit these files by hand. CI fails
  when they are stale (`TestDocsCodeTableMatchesRegistry`).
- Go and JS check the same rules with the same codes and messages. Put a new
  rule or warning in both, with a case in `types/builder/testdata/`
  (`validation-corpus.json`, `warning-corpus.json`).
- Add each route and method to `src/go/web/public/docs/openapi.yml`
  (`TestBuilderRoutesDocumented`). Each route refuses no identity, a
  configs-only role, and Global Viewer (`TestBuilderRoutesNeedTheBuilder`).
- `builderAllowed` stays a literal `role.Allowed("builder", "use")` call, so
  the generator of `web/rbac/known_policy_gen.go` finds it. The Builder role
  (`api/config/default/builder.yml`) never grants `*/*`.
- An item button (Edit, Delete, Publish, Duplicate, Open, View, Download) is
  `BuilderActionButton.vue` with an action from `builder/itemActions.js`.
- Focus never falls to `<body>`, and a dialog gives focus back to its opener.
- The phenix skill (`skills/phenix/`) leaves out the Builder on purpose. Do
  not add Builder text to it: agents read the user docs instead.

### Gotchas

- Do not decode client text with `store.NewConfigFromYAML`. It expands
  `${NAME}` from the server environment. Use `store.DecodeConfigJSON` or
  `store.DecodeConfigYAML`.
- Preflight lists bridges with `ovs-vsctl list-br` (`mm.GetBridges`). Do not
  use the minimega `bridge` command: it creates the default bridge and
  rewrites the `bridges` file.
- Only `forkLocalHistory` (`builder/autosave.js`) sends `forkOf`, not
  `store.duplicateDraft`. On the server, `forkOrigin`
  (`web/builder_publish.go`) gives the new draft the source token of the
  `forkOf` draft, so a duplicate with `forkOf` could publish to the topology
  of the original.
- These hold `builderPublishLock`: Publish, the deletion of a draft or of a
  published document (`web/builder_documents.go`), `POST /builder/cleanup`,
  and the deletion or rename of a Topology config (`lockBuilderPublishing`,
  called from `web/config.go`). Do not call one of them while you hold the
  lock.
- The Scenario config route replaces all annotations on `PUT`. Make the body
  of a Scenario update with `replacedScenarioConfig(stored, uploaded)`
  (`builder/publish.js`), which keeps the stored annotations and merges the
  `topology` annotation.
- Keep devices in source order. Do not sort them: minimega starts the VMs in
  that order.
- When JS compares or folds a name that Go also compares, use
  `goEqualFold`, `goToLower`, or `foldKey` (`builder/goCase.js`), not
  `toLowerCase`. The case tests (`types/builder/case_test.go`) skip on a Go
  with another Unicode version. Run them with Go 1.24 or 1.25.
- Code that places, counts, or colors nodes by layer uses
  `effectiveNodeLayer`, not `nodeLayer`.
- Do not bind the Vue Flow `snapToGrid` prop. It snaps each node when it
  mounts, and that moves drafts that are off the grid.
- Show icon bytes only through an `<img>` with a `data:image/png;base64,`
  URL, never as markup.
- If you move `Builder.vue`, update the view that `src/js/vite.config.js`
  gives `src/js/plugins/builder-assets.js`. The rest of the UI loads `builder/session.js`,
  `focusMode.js`, and `configs.js` uncompressed, so keep their imports small
  (`src/js/test/builder/session.test.js` checks the first two).

### Checks

- In `src/go`: `go test -race ./types/builder ./api/builder` and
  `go test -race ./web -run Builder`
- In `src/js`: `npm test -- test/builder`

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
`E2E_BASE_URL=http://127.0.0.1:3080`.

## Documentation and References

| Need | Reference |
|---|---|
| Build, install, logging, config | `README.md` |
| App development | `examples/README.md`, `examples/TUTORIAL.md` |
| Config interfaces and fields | `src/go/types/interfaces/`, `src/go/types/version/` |
| YAML config schemas | `src/go/types/version/schemas/{v0,v1,v2}.yaml`, one file per `apiVersion` holding the kinds stored at it: `v0` `Workflow`, `v1` `Topology`/`Experiment`/`Image`/`User`/`Role`, `v2` `Scenario` |
| CLI and REST implementation | `src/go/cmd/`, `src/go/web/server.go` |
| Builder | [Builder](#builder), [`docs/content/builder/`](docs/content/builder/) |
| Narrative docs source | [`docs/`](docs/) |
| Internet-hosted narrative docs | [phenix.sceptre.dev](https://phenix.sceptre.dev/latest/) |
| minimega commands and behavior | [API docs](https://sandia-minimega.github.io/minimega/reference/minimega/), [source](https://github.com/sandia-minimega/minimega) |
| Official apps and SCORCH components | [`sceptre-phenix-apps`](https://github.com/sandialabs/sceptre-phenix-apps) |
| Image configs, overlays, scripts | [`sceptre-phenix-images`](https://github.com/sandialabs/sceptre-phenix-images) |
| Source for phenix.sceptre.dev | [`docs/`](docs/) |
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

GitHub Actions is path-scoped: `ci.yml` generates, lints, and tests Go and
checks the Go example; `frontend.yml` runs knip and Vitest; `e2e.yml` builds
UI/backend for each auth mode and runs the Playwright tests, for changes that
can affect the browser (its path filters); `examples.yml` checks the Python
example; `packages.yml` builds the Docker image (Buildx with the GHA layer
cache), JIT UI image, and Debian package; `podman.yml` builds the same
`docker/Dockerfile` with Podman on each push to `main` and on pull requests that
change the container files; `docs.yml` builds the documentation site for pull
requests and publishes it to GitHub Pages from `main` (only its deploy job can
write). `lint.yml` runs every prek hook on all changes. `ci.yml`,
`frontend.yml`, `lint.yml`, and `examples.yml` run on pushes to `main` and on
pull requests only, so a push to an open pull request starts each run once; use
`workflow_dispatch` for a branch with no pull request. Dependabot keeps the
pinned actions current. Update affected path filters, inputs, generated
artifacts, tool versions, and local-equivalent commands together. Preserve
least-privilege permissions, supported action versions, lockfile caches, and
shared version values.

Do not publish artifacts, push images, or create releases under any
circumstances. The only exception is packages that a fork's own workflows
publish to that fork's registry when you push a branch to the fork (for example
`ghcr.io/<fork-owner>/sceptre-phenix/phenix:<branch>`); pushing branches to a
fork is allowed.
