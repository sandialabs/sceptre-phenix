# Workflow Reference

`phenix workflow apply` deploys a topology directory through the running
`phenix ui`: it stages the directory's injects, upserts its configs, and
applies its workflow config. This file holds the command, its workflow config,
its REST endpoints, gotchas, and troubleshooting. The
[skill body](../SKILL.md) covers the Topology, Scenario, and Experiment
configs it deploys, and the
[Workflow docs page](https://phenix.sceptre.dev/latest/workflow/) has the
validation error format and the curl walkthrough.

## `phenix workflow` — deploy a topology directory through the running server

```bash
phenix workflow apply helloworld                 # look helloworld up under base-dir.topologies; works from any directory
phenix workflow apply helloworld -n              # --dry-run: validate everything, log the server's plan, change nothing
phenix workflow apply helloworld -f              # --force: allow restarting the running experiment
phenix workflow apply ./site -b lab-a -c alt.yml # --branch-name: branch name (default: the directory's name); --config: workflow config, relative to DIR unless absolute
```

`phenix workflow apply --help` lists more examples. The argument is required;
use `apply .` for the current directory. An existing path is used as given;
otherwise a bare `NAME` resolves to `<base-dir.topologies>/NAME`.

```text
helloworld/
├── phenix.yml          # workflow config; another file name needs -c
├── phenix-configs/     # configs, one document per .json, .yaml or .yml file; other and hidden files are skipped
│   ├── topology.yml
│   └── scenario.yml
└── phenix-injects/     # copied to <base-dir.injects>/<name>, e.g. /phenix/injects/helloworld
    └── motd
```

Every part of the directory is optional, and a step whose input is missing is
skipped. A directory with none of them is an error. Every file in
`phenix-configs/` holds one YAML or JSON document, and its `kind` must be
written out, not given as a `${VAR}` placeholder.

`<name>` is the workflow branch name: the directory's name unless `-b` sets
it. It must match `^[A-Za-z0-9][A-Za-z0-9._-]*$`. The server expands
`${BRANCH_NAME}` placeholders (for example
`/phenix/injects/${BRANCH_NAME}/motd`) to it, and fills in other `${VAR}`
placeholders from its own environment. Configs that hard-code the injects
directory need `-b` set to the name they use.

The names `phenix.yml`, `phenix-configs` and `phenix-injects` are reserved, in
any letter case. The branch name cannot be one of them, and no entry of
`phenix-injects/` may have one of them at its top level or directly inside one
of its top-level directories.

The command runs these steps, named in the `step` field of its log lines, and
nothing changes before `injects`:

1. **`preflight`.** The server must answer on `--unix-socket`, and every
   config and the workflow config must parse.
2. **`plan`.** The server dry-runs each config and then the workflow config,
   and the plan is logged. `-n` stops here with exit status 0. Without `-n`,
   a `restart` plan fails unless `-f` is given, and nothing has changed.
3. **`injects`.** `phenix-injects/` is copied to `<base-dir.injects>/<name>`,
   replacing the previous copy. The server's `base-dir.injects` is used unless
   `--base-dir.injects` is given on the command line. Staging needs write
   access there, so run the command as the user that runs `phenix ui`.
4. **`configs`.** Each config is created or updated, topologies first.
5. **`apply`.** The workflow config is applied, with the tags
   `method=workflow`, `dir`, `branch`, `workflow_date` and, in a git work
   tree, `commit` (with `-dirty` for uncommitted changes). They are stored in
   the experiment's `phenix.workflow/tags` annotation.

Output goes through the phenix logger; there are no prompts. The exit status
is 0 on success or after a dry run, and non-zero otherwise.

Workflow config (`phenix.yml`) reference. It is validated against the
`Workflow` schema in `src/go/types/version/schemas/v0.yaml`, served at
`GET /schemas/workflow/v0`: an unknown or misspelled key (keys are
case-sensitive), a value of the wrong type, or a missing `spec` is rejected
with 400. `metadata` is not used today; it is accepted so a workflow config
has the same top-level keys as every other phenix config, and so a later
version can add a name or annotations without a breaking change.

```yaml
apiVersion: phenix.sandia.gov/v0   # required
kind: Workflow                     # required
metadata: {}
spec:                              # required; {} applies every default
  auto:
    create: ${BRANCH_NAME}         # experiment to create when none is mapped to the branch; omit to never create
    update: true                   # default true: update the mapped experiment
    restart: true                  # default true: restart it if running; start it after a create or update
  topology: ${BRANCH_NAME}         # topology config; needed to create, else defaults to the experiment's
  scenario: ${BRANCH_NAME}         # scenario config (optional)
  vlans:
    EXP: 101                       # VLAN alias -> VLAN ID
  schedules:
    host-01: compute1              # VM hostname -> cluster host
  deployMode: all                  # all, no-headnode or only-headnode; omit for the server's deploy-mode
  useGREMesh: false
  vlanRange:
    min: 100                       # 0 leaves that end of the range unset
    max: 200
  defaultBridge: phenix            # at most 15 characters (the Linux interface-name limit)
```

Experiments are mapped to a branch by their `phenix.workflow/branch`
annotation. What apply does:

| Mapped experiments | Condition | Action |
|---|---|---|
| 0 | `auto.create` not set | `none` |
| 0 | `auto.create` isn't a valid experiment name | 400 |
| 0 | `auto.create` names an existing experiment that isn't mapped | 409 |
| 0 | `auto.create` set, `auto.restart` true / false | `createAndStart` / `create` |
| 1 | `auto.update` false | `none` |
| 1 | running, `auto.restart` false | `none` |
| 1 | running, `auto.restart` true | `restart` (needs `-f` in the CLI) |
| 1 | stopped, `auto.restart` true / false | `updateAndStart` / `update` |
| more than 1 | any | 409 |

Before anything changes, the server checks the workflow config, the plan, and
the topology and scenario it would use, including the topology's nodes. A
failure is a 400 (409 for a plan conflict) and a running experiment keeps
running. Validation errors are explained one per line, as
`<item> "<hostname|name>" (line N): <what> (at <sub-path>)`; HTTP errors carry
these lines in `metadata.validation` and the validator's text in
`metadata.validation-raw`.

## REST endpoints

Paths are relative to the `/api/v1` base path; [`web-api.md`](web-api.md) has
the full route table and the auth modes.

| Route | Notes |
|---|---|
| `POST /workflow/configs/{branch}[?dryRun=true]` | Create or update a config; `${BRANCH_NAME}` expands to `{branch}`. The dry run stores nothing and returns JSON `{action, kind, name, dryRun, pending}`. |
| `POST /workflow/apply/{branch}[?dryRun=true][&pending=<ref>][&expect=<action>][&tag=key=value]` | Returns JSON `{action, experiment, reason, dryRun}`. `pending`, only on a dry run, is a config dry run's `pending` value, url-encoded, and names a config the caller stores before the real apply. A plan other than `expect` is a 409 that changes nothing. |
| `GET /options` | Server-side CLI defaults, plus `base-dir.injects` and `workflow-dry-run`. |

## Gotchas

- **`phenix workflow apply` only talks to a server on the same host.** It uses
  the unix socket, and it copies injects straight into the server's
  `base-dir.injects`. Under Docker, run it with `docker exec phenix ...` and
  pass a path under `/phenix` or a bare `NAME` from `/phenix/topologies`; a
  relative path resolves inside the container.
- **Under `docker exec` the `commit=` tag is left out.** The command runs as
  root on a clone that a host user owns, so git refuses the repository; the
  command warns and applies without the tag. Add the directory to git's
  `safe.directory` for that user to keep it.
- **`-n` can't find every error.** It checks only the schema of an
  `Experiment` config, and it doesn't merge the topologies that a not yet
  stored topology includes. The real run can then fail after the injects are
  staged and the configs upserted, but before any experiment has changed.
- **Don't add an `Experiment` config named like the workflow's `auto.create`
  to `phenix-configs/`.** The real apply then finds an experiment that isn't
  mapped to the branch and refuses with 409. Let `auto.create` create it.
- **`workflow:create` lets a caller probe the server's files.** An apply dry
  run can show whether a path exists on the server. Grant the permission only
  to trusted roles; the default policies give it to Global Admin only.
- **Output is log lines, not stdout.** With `log.level` above `info` or
  `log.console` set to a file, the plan and result aren't on the console; use
  the exit status.
- **How the dry run sees configs that aren't stored yet.** Each config's dry
  run returns a `pending` ref (`<Kind>/<name>`, plus the references the config
  makes). The apply dry run sends those refs, and the server plans as if the
  configs were stored. `preflight` refuses a server that doesn't report
  `workflow-dry-run` in `GET /options`, because it would store the configs
  during the dry run.

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| `phenix workflow apply` refuses the server | The server doesn't report `workflow-dry-run` in `GET /options`, or answered with an action the command doesn't know; use the same release for `phenix ui` and the CLI. |
| `re-run with -f` | The plan would restart a running experiment. Nothing has changed; re-run with `-f` to allow it. |
| `phenix server unreachable` | Nothing answered on `--unix-socket`: `phenix ui` isn't running, listens on another socket, or didn't answer in time. In `configs` or `apply`, check the experiment. |
| `connect: permission denied; run phenix as the user that runs phenix ui` | Your user may not write to the socket. Run the command as the user that runs `phenix ui`, usually root (or through `docker exec` with Compose), or start `phenix ui` with `--unix-socket-gid` set to the ID of a group you are in. Nothing was sent. |
| `nothing to apply` | The directory has none of `phenix-injects/`, `phenix-configs/` or `phenix.yml`, or only a `phenix-configs/` without a config file; check the path or name. |
| `<file> and <file> both define <Kind>/<name>` | Two files in `phenix-configs/` name the same config once the server fills in their placeholders. Rename or remove one. Nothing was changed. |
| `already changed: ...; fix the cause and run the command again` | The run failed after it staged injects or upserted configs. Fix the cause and run the command again; it stages and upserts everything again. |
