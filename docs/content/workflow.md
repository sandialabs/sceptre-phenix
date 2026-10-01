# Workflow

A **topology directory** holds everything phēnix needs to run an experiment:
its configs, the files to inject into its VMs, and a **workflow config**
(`phenix.yml`) that says whether to create, update, start or restart the
experiment. `phenix workflow apply` deploys such a directory in one command,
and CI jobs and scripts can call the same [REST API](#rest-api) endpoints.

!!! note
    `phenix workflow apply` runs on the phēnix host, or in the phēnix
    container, and talks to the running `phenix ui` server through its unix
    socket. Use a server from the same phēnix release as the CLI.

## Quick Start

Create a topology directory with a topology config, a file to inject and a
workflow config:

```bash
mkdir -p /phenix/topologies/helloworld/phenix-configs /phenix/topologies/helloworld/phenix-injects
cd /phenix/topologies/helloworld

echo "hello from phenix workflow apply" > phenix-injects/motd

cat > phenix-configs/topology.yml <<'EOF'
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: ${BRANCH_NAME}
spec:
  nodes:
  - type: VirtualMachine
    general:
      hostname: host-01
    hardware:
      os_type: linux
      drives:
      - image: ubuntu.qc2
    network:
      interfaces:
      - name: IF0
        vlan: EXP
        address: 10.0.0.1
        mask: 24
        proto: static
        type: ethernet
    injections:
    - src: /phenix/injects/${BRANCH_NAME}/motd
      dst: /etc/motd
EOF

cat > phenix.yml <<'EOF'
apiVersion: phenix.sandia.gov/v0
kind: Workflow
metadata: {}
spec:
  auto:
    create: ${BRANCH_NAME}
  topology: ${BRANCH_NAME}
EOF
```

Check what phēnix would do, then deploy:

```bash
phenix workflow apply helloworld -n
phenix workflow apply helloworld
phenix experiment list
```

The dry run logs the plan (`action=createAndStart`, `experiment=helloworld`)
and changes nothing. The second command copies `phenix-injects/` to
`/phenix/injects/helloworld`, creates the `helloworld` topology, and creates
and starts the `helloworld` experiment. After you change the topology, run it
again with `-f` to allow restarting the running experiment:

```bash
phenix workflow apply helloworld -f
```

## Topology Directory

```text
helloworld/
├── phenix.yml          # workflow config
├── phenix-configs/     # topology, scenario and other configs
│   ├── topology.yml
│   └── scenario.yml
└── phenix-injects/     # files to inject into VMs
    └── motd
```

Every part is optional, and a missing part is skipped: a directory with only
`phenix-configs/` just creates or updates its configs. A directory with none
of them is an error.

* `phenix.yml` - the [workflow config](#workflow-config). A file with another
  name needs `-c`.
* `phenix-configs/` - one config per `.json`, `.yaml` or `.yml` file, in
  subdirectories too. Other files and hidden files are skipped. Each file
  holds one YAML or JSON document, and its `kind` must be written out, not
  given as a `${VAR}` placeholder. Each config is created, or updated if it
  already exists.
* `phenix-injects/` - copied to `<base-dir.injects>/<name>` on the phēnix
  host, by default `/phenix/injects/<name>`, replacing any previous copy.

### Branch Name

`<name>` is the **workflow branch name**: the directory's name, unless `-b`
sets another. It must start with a letter or digit and hold only letters,
digits, `.`, `_` and `-`.

`${BRANCH_NAME}` in a config or in the workflow config expands to the branch
name; other `${VAR}` placeholders are filled in from the server's environment
(see [Environment Variables](configuration.md#environment-variables)). That's
why configs reference injected files as
`/phenix/injects/${BRANCH_NAME}/<file>`, and why one directory can be deployed
under several names side by side. An experiment that a workflow creates is
[mapped](#actions) to its branch name.

Configs that hard-code the injects path need `-b` set to the name they use:
a topology that references `/phenix/injects/lab-a/<file>` is deployed with
`phenix workflow apply ./site -b lab-a`.

The names `phenix.yml`, `phenix-configs` and `phenix-injects` are reserved, in
any letter case. A branch name can't be one of them, and they can't appear at
the top level of `phenix-injects/` or directly inside one of its top-level
directories.

## The `phenix workflow apply` Command

```bash
phenix workflow apply <DIR|NAME> [flags]
```

`DIR` is the path of a topology directory; use `.` for the current directory.
A `NAME` that isn't an existing path is looked up under `base-dir.topologies`,
by default `/phenix/topologies/NAME`, so the command works from any directory.

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-b, --branch-name` | the directory's name | The workflow [branch name](#branch-name). |
| `-c, --config` | `phenix.yml` | Workflow config file, relative to the directory unless absolute. |
| `-n, --dry-run` | `false` | Validate everything, log the server's plan, and change nothing. |
| `-f, --force` | `false` | Allow restarting a running experiment. |

The global flags `--unix-socket`, `--base-dir.injects`, `--base-dir.topologies`
and `--log.level` also apply; see [Global Flags](settings.md#global-flags).

The command never prompts. It reports through the phēnix logger and runs five
steps, named in the `step` field of its log lines. Nothing changes before
`injects`:

1. **`preflight`** - checks that the server answers and that every file can
   be read and parsed.
2. **`plan`** - the server [dry-runs](#dry-run) every config and the workflow
   config, and the plan is logged. `-n` stops here. Without `-f`, so does a
   plan that would restart a running experiment.
3. **`injects`** - copies `phenix-injects/` into place.
4. **`configs`** - creates or updates each config, topologies first.
5. **`apply`** - applies the workflow config: the server creates, updates,
   starts or restarts the experiment (see [Actions](#actions)).

The exit status is 0 when the run completed or a dry run passed, and non-zero
otherwise.

The apply records where it came from in the experiment's
`phenix.workflow/tags` annotation: `method=workflow`, the directory (`dir`),
the branch name (`branch`), the time (`workflow_date`) and, when the directory
is in a git work tree, the short `commit`, with a `-dirty` suffix for
uncommitted changes.

!!! warning
    Run the command from outside a topology directory you didn't write.
    phēnix reads its settings files (`config.*` and `users.*`) from the
    current directory first, so such a file in the directory could change the
    settings of the run.

### Examples

`phenix workflow apply --help` has the full list.

| Command | What it does |
| :--- | :--- |
| `phenix workflow apply /phenix/topologies/helloworld` | Deploy by path. The branch name is `helloworld`. |
| `phenix workflow apply helloworld` | Look up `helloworld` under `base-dir.topologies`. |
| `phenix workflow apply .` | Deploy the current directory. |
| `phenix workflow apply helloworld -n` | Validate everything and log the plan. |
| `phenix workflow apply helloworld -f` | Allow restarting the running experiment. |
| `phenix workflow apply helloworld -b helloworld-2` | Deploy a second copy under another branch name. |
| `phenix workflow apply helloworld -c alt.yml` | Use another workflow config from the directory. |
| `docker exec phenix phenix workflow apply helloworld -f` | Run it in a [Docker Compose](#docker-compose) deployment. |

```bash
# In CI, deploy a checkout under its git branch name. "/" isn't allowed in a
# workflow branch name, and the guard stops the command when BRANCH is unset
: "${BRANCH:?}" && phenix workflow apply ./site -b "${BRANCH//\//-}" -f

# Check every topology directory without changing anything
for dir in /phenix/topologies/*/; do
  phenix workflow apply "$dir" -n || echo "check $dir"
done
```

## Workflow Config

The workflow config is a phēnix config of kind `Workflow`. It isn't stored in
phēnix; it's sent with each apply.

```yaml
apiVersion: phenix.sandia.gov/v0
kind: Workflow
metadata: {}
spec:
  auto:
    create: ${BRANCH_NAME}
    update: true
    restart: true
  topology: ${BRANCH_NAME}
  scenario: ${BRANCH_NAME}
  vlans:
    EXP: 101
  schedules:
    host-01: compute1
  deployMode: all
  useGREMesh: false
  vlanRange:
    min: 100
    max: 200
  defaultBridge: phenix
```

| Field | Default | Description |
| :--- | :--- | :--- |
| `apiVersion` | required | `phenix.sandia.gov/v0`. |
| `kind` | required | `Workflow`. |
| `metadata` | | Not used today. It is accepted so a workflow config has the same top-level keys as every other phēnix config, and so a later version can add a name or annotations without a breaking change. |
| `spec` | required | Holds the fields below; `spec: {}` applies every default. |
| `auto.create` | unset | The name of the experiment to create when none is mapped to the branch. Unset never creates one. |
| `auto.update` | `true` | Update the experiment mapped to the branch. A running experiment is only updated if `auto.restart` is also true. |
| `auto.restart` | `true` | Restart a running experiment to update it, and start an experiment that was just created or updated. |
| `topology` | the experiment's topology | The topology config to use. Required to create an experiment. |
| `scenario` | the experiment's scenario | The scenario config to use, if any. |
| `vlans` | | VLAN alias to VLAN ID. |
| `schedules` | | VM hostname to cluster host. |
| `deployMode` | the server's `deploy-mode` | Which minimega VMs to deploy: `all`, `no-headnode` or `only-headnode`. |
| `useGREMesh` | `false` | Use GRE tunnels between mesh nodes for VLAN trunking. |
| `vlanRange.min`, `vlanRange.max` | `0` | The lowest and highest VLAN IDs minimega may assign. `0` leaves that end unset. |
| `defaultBridge` | `phenix` | The bridge VMs connect to by default: at most 15 characters and, unless it's `phenix`, used by no other experiment. |

### Schema

The config is validated against the `Workflow` schema in
[`v0.yaml`](https://github.com/sandialabs/sceptre-phenix/blob/main/src/go/types/version/schemas/v0.yaml),
which a running server serves at `GET /api/v1/schemas/workflow/v0`. An unknown
or misspelled key, a value of the wrong type or a missing `spec` is rejected
with a [validation error](#validation-errors) and changes nothing.

### Actions

An experiment is **mapped** to a branch when its `phenix.workflow/branch`
annotation is the branch name. phēnix sets it on the experiments a workflow
creates. The server plans one action from the experiments mapped to the
branch:

| Mapped experiments | Condition | Action |
| :--- | :--- | :--- |
| 0 | `auto.create` not set | `none` |
| 0 | `auto.create` set, `auto.restart` true | `createAndStart` |
| 0 | `auto.create` set, `auto.restart` false | `create` |
| 1 | `auto.update` false | `none` |
| 1 | running, `auto.restart` false | `none` |
| 1 | running, `auto.restart` true | `restart` (needs `-f` in the CLI) |
| 1 | stopped, `auto.restart` true | `updateAndStart` |
| 1 | stopped, `auto.restart` false | `update` |
| more than 1 | any | error |

It is also an error when `auto.create` names an existing experiment that
isn't mapped to the branch.

## Dry Run

`phenix workflow apply -n` asks the server to check everything the command
would send, and changes nothing. A real apply runs the same checks before it
changes anything, so a mistake in a config never stops a running experiment.
The server checks that:

* each config passes its schema, and each topology's nodes pass the checks an
  experiment start makes, such as duplicate or reserved hostnames;
* the workflow config passes its [schema](#schema);
* the topology and scenario the workflow config names exist and, to create an
  experiment, that the scenario is annotated for the topology;
* the plan has no conflict (see [Actions](#actions)).

A first deploy can be checked too: the topology and scenario may come from
the directory's `phenix-configs/` before they are stored in phēnix.

## Validation Errors

When a config fails schema validation, phēnix explains each error in one line:

```text
<item> "<hostname|name>" (line N): <what> (at <sub-path>)
```

`<item>` is the list element that holds the error, such as `nodes[1]`, with
its hostname or name, and `<sub-path>` is the path inside it. When a missing
key exists elsewhere in the same item, usually because it's indented at the
wrong level, a `hint:` line follows.

For example, a topology where the second node's `image` is one level too high:

```yaml linenums="1"
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: helloworld
spec:
  nodes:
  - type: VirtualMachine
    general:
      hostname: host-01
    hardware:
      os_type: linux
      drives:
      - image: ubuntu.qc2
  - type: VirtualMachine
    general:
      hostname: host-02
    hardware:
      os_type: linux
      image: ubuntu.qc2
      drives:
      - interface: virtio
```

```text
nodes[1] "host-02" (line 21): property "image" is missing (at hardware.drives[0].image)
  hint: "image:" is on line 19 under nodes[1].hardware, but the schema expects it at nodes[1].hardware.drives[0].image
```

These lines appear in the output of `phenix workflow apply` and
`phenix config create`, in the web UI's config editor and upload dialog, and
in the `metadata.validation` field of API errors. The validator's original
text is in `metadata.validation-raw`.

## REST API

The command is a client of two endpoints, which CI jobs and scripts can call
directly. The [API docs](api.md) list every parameter and status under the
`Workflow` tag.

| Endpoint | Description |
| :--- | :--- |
| `POST /api/v1/workflow/configs/{branch}` | Create or update one config. |
| `POST /api/v1/workflow/apply/{branch}` | Apply the workflow config and return the [action](#actions) as JSON. |

Send the file as the body, with `Content-Type: application/x-yaml` or
`application/json`. When authentication is enabled, add the
`X-phenix-auth-token: Bearer <token>` header (see
[API](api.md#authentication)).

| Query parameter | Endpoint | Description |
| :--- | :--- | :--- |
| `dryRun=true` | both | Run every check and change nothing. |
| `pending=<ref>` | apply, dry run only | A config you will store before the real apply; `<ref>` is the `pending` value its config dry run returned. Repeat it for more configs. |
| `expect=<action>` | apply | Return 409 and change nothing when the server plans another action. |
| `tag=<key>=<value>` | apply | A tag to store on the experiment. Repeat it for more tags. |

This is what `phenix workflow apply` does, without the injects, for a
directory with one topology config:

```bash
API=http://localhost:3000/api/v1/workflow
YAML='Content-Type: application/x-yaml'

# 1. Check the config
curl -sS --fail-with-body -X POST -H "$YAML" --data-binary @phenix-configs/topology.yml "$API/configs/helloworld?dryRun=true"

# 2. Plan the apply, naming the config that step 3 stores
curl -sS --fail-with-body -X POST -H "$YAML" --data-binary @phenix.yml \
  "$API/apply/helloworld?dryRun=true&pending=Topology/helloworld"

# 3. Store the config
curl -sS --fail-with-body -X POST -H "$YAML" --data-binary @phenix-configs/topology.yml "$API/configs/helloworld"

# 4. Apply, only if the plan is still the one step 2 returned
curl -sS --fail-with-body -X POST -H "$YAML" --data-binary @phenix.yml "$API/apply/helloworld?expect=createAndStart"
```

The apply answers with its plan:

```json
{"action":"createAndStart","experiment":"helloworld","reason":"","dryRun":false}
```

On the phēnix host, the same endpoints are served on the unix socket without
authentication:

```bash
curl -sS --unix-socket /tmp/phenix.sock -X POST -H 'Content-Type: application/x-yaml' \
  --data-binary @phenix.yml 'http://unix/api/v1/workflow/apply/helloworld?dryRun=true'
```

## Docker Compose

With Docker Compose, the unix socket is inside the `phenix` container, so run
the command there:

```bash
docker exec phenix phenix workflow apply helloworld -n
docker exec phenix phenix workflow apply /phenix/topologies/helloworld -f
```

Keep topology directories under `/phenix`, which is mounted at the same path
in the container, and pass an absolute path or a bare name: a relative path
resolves inside the container. To change a [setting](#settings), set its
environment variable in the `phenix` service and recreate the container.

## Settings

| Setting | Default | Description |
| :--- | :--- | :--- |
| `base-dir.injects` | `<base-dir.phenix>/injects` | Where injects are copied to. The server's value is used unless `--base-dir.injects` is given on the command line. |
| `base-dir.topologies` | `<base-dir.phenix>/topologies` | Where `phenix workflow apply NAME` looks for `NAME`. |

Use absolute paths for both. See the
[Settings Reference](settings.md#settings-reference) for how to set them.

## Limitations

* The command only works on the phēnix host or in the phēnix container. A CI
  job on another machine can call the REST API, but has to get the injects
  onto the phēnix host itself.
* There is no web UI for workflow apply.
* Avoid sending workflow requests for different branches at the same moment:
  the server sets `BRANCH_NAME` for the whole process while it parses a
  request.
* A dry run can't find every error. It doesn't merge the topologies that a
  not yet stored topology includes, and it checks only the schema of an
  `Experiment` config. The real apply can then fail after the injects are
  copied and the configs stored, but before any experiment has changed.
* Don't add an `Experiment` config named like the workflow's `auto.create` to
  `phenix-configs/`; let `auto.create` create the experiment.
* Grant the `workflow:create` permission only to trusted roles: an apply dry
  run can reveal whether a file exists on the server.

## Troubleshooting

* **Permission denied.** Your user may not write to the unix socket or to the
  injects directory. Run the command as the user that runs `phenix ui`,
  usually root (with Docker Compose, through `docker exec`), or start
  `phenix ui` with `--unix-socket-gid` set to the ID of a group you are in.
* **The server doesn't answer.** Check that `phenix ui` is running and
  listens on the socket that `--unix-socket` names, `/tmp/phenix.sock` by
  default.
* **The server is refused.** The server is from another phēnix release than
  the CLI; use the same release for both.
* **The run stops and asks for `-f`.** The plan would restart a running
  experiment. Nothing was changed; run it again with `-f` to allow it.
* **The plan changed (409).** The experiment was started or stopped between
  the `plan` and `apply` steps. Run the command again.
* **The injects destination is refused.** The destination overlaps a topology
  directory, or the server reports a relative `base-dir.injects`. Check
  `base-dir.injects` and `-b`.
* **A run stopped after it changed something.** A run that stops in or after
  `injects` leaves the injects and the configs it stored in place, and its
  error says what was changed. Fix the cause and run the command again. Only
  a run that stops in `apply` can have changed the experiment.
* **Leftovers next to the injects.** Hidden `.<name>.tmp-*` and
  `.<name>.old-*` entries in `<base-dir.injects>` are left by an interrupted
  run. Remove them by hand when no other run is active.
* **No `commit` tag.** Git refuses a repository that another user owns, which
  is common under `docker exec`. Add the directory to git's `safe.directory`
  for the user that runs the command.
* **No output.** With `log.level` at `warn` or higher, the plan and the result
  aren't printed; rely on the exit status.

## How It Works

* **Server check.** In `preflight` the command reads `GET /api/v1/options`
  from the unix socket. The server must report `"workflow-dry-run": true`: an
  older server would store configs during a dry run. The answer also carries
  the server's `base-dir.injects`.
* **Pending configs.** In `plan`, each config's dry run returns a `pending`
  ref such as `Topology/helloworld`. The workflow config's dry run sends these
  refs, so the server plans as if the configs were stored and checks the
  references between them.
* **Injects.** `phenix-injects/` is copied to a temporary directory next to
  `<base-dir.injects>/<name>` and swapped into place, so a failed copy leaves
  the previous copy intact. Symbolic links are copied as links, and only
  permission bits are kept.
* **Apply.** The real apply sends `expect=<the planned action>`, so the
  server changes nothing when the plan differs from the one that was checked.
