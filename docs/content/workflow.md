# Workflow

A **topology directory** holds everything phēnix needs to run an experiment:
its configs, the files to inject into its VMs, and a **workflow config**
(`phenix.yml`) that says whether to create, update, start or restart the
experiment. `phenix workflow apply` deploys such a directory in one command,
and `phenix workflow delete` removes the deployment again.
It runs on the phēnix host (or in the phēnix container) and talks to the
running `phenix ui` server through its unix socket. CI jobs and scripts can
call the same [REST API](#api).

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

Do a dry run, then deploy for real:

```bash
phenix workflow apply helloworld -n
phenix workflow apply helloworld
phenix experiment list
```

The dry run logs the plan (`action=createAndStart`, `experiment=helloworld`)
and changes nothing. The real run copies `phenix-injects/` to
`/phenix/injects/helloworld`, creates the `helloworld` topology, and creates
and starts the `helloworld` experiment. After you change the topology, add
`-f` so phēnix may restart the running experiment:

```bash
phenix workflow apply helloworld -f
```

## Topology Directory

```text
helloworld/
├── .env                # variables for the configs (optional)
├── phenix.yml          # workflow config
├── phenix-configs/     # topology, scenario and other configs
│   ├── topology.yml
│   └── scenario.yml
└── phenix-injects/     # files to inject into VMs
    └── motd
```

Each part is optional and a missing part is skipped, but a directory with
none of them is an error.

* `.env` - the [variables](#variables) for the placeholders in the configs
  and in the workflow config, as `NAME=value` lines.
* `phenix.yml` - the [workflow config](#workflow-config). Use `-c` for another
  name, such as `phenix.yaml`. An older `.phenix.yml` is read when there's no
  `phenix.yml`, and having both is an error.
* `phenix-configs/` - one config per `.json`, `.yaml` or `.yml` file,
  subdirectories included (hidden files are skipped). Each file holds one
  document with a `kind`. Each config is created or updated, topologies first.
* `phenix-injects/` - copied to `<base-dir.injects>/<name>` on the phēnix
  host (by default `/phenix/injects/<name>`), replacing the previous copy.
  By default, symlinks inside `phenix-injects/` are copied as symlinks;
  `--follow-inject-symlinks` follows them and copies targets as regular files
  or directories.

### Branch Name

`<name>` is the **workflow branch name**: the directory's name, or what you
set with `-b`. It holds only letters, digits, `.`, `_` and `-`, and starts
with a letter or digit. `phenix.yml`, `phenix-configs` and `phenix-injects`
are reserved, in any letter case: not as the branch name, and not in the top
two levels of `phenix-injects/`.

The branch name decides where the injects go, what `${BRANCH_NAME}` expands
to in the configs and the workflow config, and which experiment the workflow
[acts on](#actions). Only `-b` sets `${BRANCH_NAME}`; every other `${VAR}`
placeholder is a [variable](#variables). Use `${BRANCH_NAME}` in names and
inject paths, as the quick start does, and you can deploy one directory under
several names. A hard-coded `/phenix/injects/lab-a/<file>` needs `-b lab-a`.

The branch name is not the experiment name. The experiment name is
`auto.create` in the [workflow config](#workflow-config). With
`auto.create: ${BRANCH_NAME}`, as in the examples on this page, `-b` also
names the experiment, and each branch name is a separate deployment. A fixed
`auto.create` ignores `-b`. phēnix reads `auto.create` only when no experiment
is mapped to the branch, so `-b` and `auto.create` do not rename an
experiment that exists.

## The `phenix workflow apply` Command

```bash
phenix workflow apply <DIR|NAME> [flags]
```

A `NAME` that isn't an existing path is looked up under `base-dir.topologies`
(by default `/phenix/topologies/NAME`).

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-b, --branch-name`, `--name` | the directory's name | The workflow [branch name](#branch-name). It does not rename an experiment. `--name` is an alias; if you give both, use the same value. |
| `-c, --config` | `phenix.yml` | Workflow config file, relative to the directory unless absolute. |
| `-e, --env` | | Set a [variable](#variables): `NAME=value`, or `NAME` to copy the value from the shell. Repeat it for more variables. |
| `--env-file` | `.env` in the directory | Read [variables](#variables) from an env file. A relative path starts at the working directory. Repeat it for more files. |
| `-n, --dry-run` | `false` | Check everything, log the server's plan, and change nothing. |
| `-f, --force` | `false` | Allow restarting a running experiment. |
| `--follow-inject-symlinks` | `false` | Follow symlinks in `phenix-injects/` and copy targets as regular files or directories. |

The global flags `--unix-socket`, `--base-dir.injects`, `--base-dir.topologies`
and `--log.level` also apply (see [Global Flags](settings.md#global-flags)).

The command never prompts. It runs five steps, and each log line names its
step in the `step` field:

1. `preflight` - checks that the server answers, fills in the
   [variables](#variables), and checks that every file parses.
2. `plan` - the server [dry-runs](#dry-run) everything, and the plan is
   logged. `-n` stops here, and so does a restart without `-f`.
3. `injects` - copies `phenix-injects/` into place. Nothing changes before
   this step.
4. `configs` - creates or updates each config.
5. `apply` - applies the workflow config (see [Actions](#actions)).

The exit status is 0 when the run completed or the dry run passed. Each apply
tags the experiment, in its `phenix.workflow/tags` annotation, with
`method=workflow`, `dir`, `branch`, `workflow_date` and the git `commit`.

!!! warning
    Don't run the command from inside a topology directory you didn't write:
    phēnix reads its settings files (`config.*` and `users.*`) from the
    current directory first.

### Examples

| Command | What it does |
| :--- | :--- |
| `phenix workflow apply /phenix/topologies/helloworld` | Deploy by path. The branch name is `helloworld`. |
| `phenix workflow apply helloworld` | Look up `helloworld` under `base-dir.topologies`. |
| `phenix workflow apply .` | Deploy the current directory. |
| `phenix workflow apply helloworld -b helloworld-2` | Deploy a second copy under another branch name. |
| `phenix workflow apply helloworld -e MEMORY=4096 -e SITE=lab` | Set [variables](#variables). `-e` overrides an env file. |
| `phenix workflow apply helloworld -e MEMORY` | Copy `MEMORY` from the shell. It then overrides `helloworld/.env`. |
| `phenix workflow apply helloworld --env-file base.env --env-file site.env` | Read the env files, and not `helloworld/.env`. `site.env` overrides `base.env`. |
| `phenix workflow render helloworld -e MEMORY=4096` | Print the configs with the variables filled in. Nothing is sent. |
| `phenix workflow apply helloworld --follow-inject-symlinks` | Follow symlinks in `phenix-injects/` and copy targets as regular files or directories. |
| `phenix workflow delete helloworld -n` | Show what [`phenix workflow delete`](#the-phenix-workflow-delete-command) would remove. Nothing changes. |
| `phenix workflow delete helloworld -f` | Stop the running `helloworld` deployment and delete it. |
| `: "${BRANCH:?}" && phenix workflow apply ./site -b "${BRANCH//\//-}" -f` | In CI, deploy a checkout under its git branch name (`/` isn't allowed in a branch name). |

## The `phenix workflow delete` Command

```bash
phenix workflow delete <NAME> [flags]
```

`NAME` is the [branch name](#branch-name) of a deployment, not a directory.
The command removes what `phenix workflow apply` deployed under that name:

1. The experiment [mapped](#actions) to the branch name. The server deletes
   it as `phenix experiment delete` does.
2. The injects directory, `<base-dir.injects>/<name>`.
3. The hidden `.<name>.tmp-*` and `.<name>.old-*` leftovers of interrupted
   runs next to it.

It doesn't remove the stored configs, such as the topology and the
scenario. Use `phenix config delete` for them.

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-n, --dry-run` | `false` | Show what the command would remove, and change nothing. |
| `-f, --force` | `false` | Stop a running experiment before the delete. |

The global flags `--unix-socket`, `--base-dir.injects` and `--log.level` also
apply, as they do for `phenix workflow apply`.

* A running experiment needs `-f`. Without it, the command removes nothing
  and exits with an error. A dry run gives the same error.
* When more than one experiment is mapped to the branch name, the command
  removes nothing and exits with an error.
* When no experiment is mapped, the command still removes the injects
  directory and the leftovers. When nothing exists for the name, it logs
  `nothing to delete for branch name <name>` and exits 0, so you can run it
  again safely.
* When the topology of another experiment injects a file from the injects
  directory, the command keeps the directory and logs a warning that names
  those experiments. Delete those experiments or change their injections,
  then run the command again. The server checks only the absolute `src`
  paths of the injections in the experiments' topologies.

With `-n`, the command writes one line for each item to standard output and
changes nothing:

```text
would stop experiment helloworld
would delete experiment helloworld
would delete directory /phenix/injects/helloworld
would delete staging leftover /phenix/injects/.helloworld.tmp-ABCDEFGHIJKLMNOPQRSTUVWXYZ
```

A kept directory shows as `would keep directory <dir>: the experiments <names>
inject files from it`.

## Variables

`phenix workflow apply` fills in the `${NAME}` and `${NAME:default}`
placeholders of each file in `phenix-configs/` and of the workflow config to
check them. Then it sends each file as written, with the variables, and the
server fills them in. The server does the same for each topology file that
`includeTopologies` names by a path (see
[Included Topology Files](#included-topology-files)). It does not change the
files in `phenix-injects/`.

The command gets the value of a variable from the first of these sources that
has it:

1. `-e NAME=value` on the command line. `-e NAME` copies the value of `NAME`
   from the shell.
2. The env files that `--env-file` names. Without `--env-file`, the command
   reads `.env` in the topology directory, if that file exists. A later file
   overrides an earlier file.
3. The shell environment, without the variables whose names start with
   `PHENIX_`. Name such a variable with `-e NAME` to use it.

The command sends only the shell variables whose names are valid variable
names, and stops when the variables are larger than 1 MiB as JSON.

For example, a topology directory can hold its defaults in `.env`:

```text title="helloworld/.env"
SITE=lab
MEMORY=2048
```

A config uses the variables, and gives a default to a variable that is
optional:

```yaml title="helloworld/phenix-configs/topology.yml (part)"
    general:
      hostname: ${SITE}-host-01
    hardware:
      memory: ${MEMORY}
      drives:
      - image: ${IMAGE:ubuntu.qc2}
```

```bash
phenix workflow apply helloworld                      # lab-host-01, 2048, ubuntu.qc2
phenix workflow apply helloworld -e MEMORY=4096       # lab-host-01, 4096, ubuntu.qc2
MEMORY=8192 phenix workflow apply helloworld          # 2048: .env overrides the shell
MEMORY=8192 phenix workflow apply helloworld -e MEMORY  # 8192: -e copies it from the shell
```

The command checks every variable in the `preflight` step, before it changes
anything:

* A placeholder with no value and no default is an error. Write `${NAME:}`
  for a variable that can be empty.
* A `-e` variable that no file uses is an error. This finds a variable name
  with a typing mistake. With a workflow config, a variable that only an
  included topology file uses counts as used: the command does this check in
  the `plan` step, after the server dry-runs the workflow config.
* A value or a default that contains a line break is an error.
* `BRANCH_NAME` can't be set as a variable. Use `-b`.

The rules for placeholders, for env files and for the logs are the same for
every command that reads config files. See
[Environment Variables](configuration.md#environment-variables) for the
syntax, the `$${NAME}` escape and the env file format.

!!! note
    The command reads the shell environment of the user that runs it. `sudo`
    removes most of that environment, and with `docker exec` the shell is that
    of the container. Use `-e`, an env file, or `docker exec -e NAME=value` in
    these cases.

### Included Topology Files

The server reads a topology file that `includeTopologies` names by a path, so
the command can't check its placeholders. The server fills them in with the
variables of the command. It gives a placeholder that no variable sets the
first of these values:

1. The environment variable of the same name on the server, with a warning.
2. The default of the placeholder.
3. An empty string, with a warning.

The server logs each warning with the file, the line and the variable, and the
command logs it in the `plan` step:

```text
/phenix/topologies/common/net.yml line 3: no variable SITE in the request or in the server environment, and the placeholder has no default; phenix used an empty string
```

A warning is for one file, variable and kind. It gives the first line and the
count of the other lines, for example `line 3 (and 2 other lines)`. The server
reports at most 50 warnings for each request, and then says how many it left
out.

As for a config, an included topology file can be at most 5 MiB after the
server fills in the variables and the branch name. A larger text is an error.

The dry run reads each topology file that the topology of the workflow config
includes, for every plan. So the `plan` step reports a line break in an
included file.

### Show the Result

`phenix workflow render` prints each config, and then the workflow config,
with the variables and the branch name filled in. The text is what the server
has after an apply. The command does not contact the server and changes
nothing. It does not read the topology files that `includeTopologies` names,
so a `-e` variable that only such a file uses is an error in `render`.

```bash
phenix workflow render helloworld -b helloworld-2 -e MEMORY=4096
```

```text
# phenix-configs/topology.yml
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: helloworld-2
...
---
# phenix.yml
apiVersion: phenix.sandia.gov/v0
kind: Workflow
...
```

The command takes `-b`, `-c`, `-e` and `--env-file`, and gives the same
variable errors as `phenix workflow apply`, except that a `-e` variable that
only an included topology file uses is an error. Its output can hold secrets: it
shows the value of each variable that a file uses.

## Workflow Config

The workflow config is a phēnix config of kind `Workflow`. It isn't stored in
phēnix; it's sent with each apply. `spec: {}` applies every default.

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
| `auto.create` | unset | The experiment to create when none is mapped to the branch. Unset, none is created. |
| `auto.update` | `true` | Update the mapped experiment. A running one is only updated if `auto.restart` is true too. |
| `auto.restart` | `true` | Restart a running experiment to update it, and start a new or updated one. |
| `topology` | the experiment's topology | The topology config. Required to create an experiment. |
| `scenario` | the experiment's scenario | The scenario config, if any. |
| `vlans` | | VLAN alias to VLAN ID. |
| `schedules` | | VM hostname to cluster host. |
| `deployMode` | the server's `deploy-mode` | `all`, `no-headnode` or `only-headnode`. |
| `useGREMesh` | `false` | Use GRE tunnels between mesh nodes for VLAN trunking. |
| `vlanRange.min`, `vlanRange.max` | `0` | The lowest and highest VLAN IDs minimega may assign. `0` leaves that end unset. |
| `defaultBridge` | `phenix` | The bridge VMs connect to by default: at most 15 ASCII letters, digits, `_`, `@`, `.` or `-`. Unless it's `phenix`, no other experiment may use it. |

### Schema

The config must match the `Workflow` schema in
[`v0.yaml`](https://github.com/sandialabs/sceptre-phenix/blob/main/src/go/types/version/schemas/v0.yaml),
also served at `GET /api/v1/schemas/workflow/v0`. An unknown key or a wrong
type gets a [validation error](#validation-errors).

### Actions

An experiment is **mapped** to a branch when its `phenix.workflow/branch`
annotation is the branch name; phēnix sets it on each experiment a workflow
creates. The server picks one action from the experiments mapped to the
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

`auto.create` is only read when no experiment is mapped, so changing it later
doesn't rename the experiment. It's an error when it names an existing
experiment that isn't mapped, so don't put an `Experiment` config by that name
in `phenix-configs/`.

A `restart` runs the start's
[disk check](experiments.md#disk-check-before-a-start) first: if the start
would fail, the apply fails and the experiment keeps running. After a start,
the scenario's `runPeriodically` apps run in an `experiments.periodic`
[background job](jobs.md).

## Dry Run

A dry run (`-n`) runs every check and changes nothing. A real apply runs the
same checks first, so a broken config never stops a running experiment. The
server checks each config's schema and each topology's nodes (such as
duplicate or reserved hostnames), the workflow config's [schema](#schema),
that the topology and scenario exist (stored or in `phenix-configs/`) and go
together, and that the plan has no conflict.

If the plan starts the experiment, the dry run also runs the
[disk check](experiments.md#disk-check-before-a-start). It reports problems as
warnings (in `warnings` in the REST response) without failing; a problem that
would stop the start begins with `the start would fail:`.

A dry run doesn't merge the topologies that an unstored topology includes,
and only checks the schema of an `Experiment` config. So the real apply can
still fail after the injects and configs are in place, but before the
experiment changes.

## Validation Errors

When a config fails schema validation, phēnix explains each error in one line:

```text
<item> "<hostname|name>" (line N): <what> (at <sub-path>)
```

When the missing key is somewhere else in the same item (usually indented at
the wrong level), a `hint:` line follows. For example, here the second node's
`image` is one level too high:

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

`phenix config create`, the web UI's config editor and API errors (in
`metadata.validation`) show the same lines.

## API

`phenix workflow apply` uses the first two endpoints, and
`phenix workflow delete` the third. The [API docs](api.md) list them under the
`Workflow` tag.

| Endpoint | Description |
| :--- | :--- |
| `POST /api/v1/workflow/configs/{branch}` | Create or update one config. |
| `POST /api/v1/workflow/apply/{branch}` | Apply the workflow config and return the [action](#actions) as JSON. |
| `DELETE /api/v1/workflow/apply/{branch}` | Delete the experiment mapped to the branch. Clients remove the injects directory after it. |

For a POST, send the file as the body with `Content-Type: application/x-yaml`
or `application/json`. DELETE takes no body. Add an
[auth token](api.md#authentication) to each request when authentication is
enabled.

The server fills in `${BRANCH_NAME}` with the branch of the URL and keeps
every other placeholder as written. A client fills in the other placeholders
before it sends a file, or sends [variables](#variables) with it.

To send variables, send a `multipart/form-data` body with two parts: a
`config` part with the file and its `Content-Type`, and a `variables` part with
a JSON object of strings. This is what `phenix workflow apply` sends. The
server fills in each placeholder of the file from the variables, and of each
[included topology file](#included-topology-files) on the apply endpoint. In
the file, a placeholder that no variable sets gets its default, or stays as
written with a warning. A variable that no file uses is not an error. A value
or a default with a line break is an error. A config or an included topology
file can be at most 5 MiB after the server fills in the variables and the
branch name. For a larger file, the answer is a 400. The
answer of a config dry run and of an apply lists the used variables in
`variables` and the warnings in `warnings`. A warning is for one file,
variable and kind, with the first line and the count of the other lines. The
answer holds at most 50 variable warnings, and then one that says how many it left out.
The server logs each warning. The audit records name the used variables, but
never their values. An apply dry run reads each topology file that the
topology of the workflow config includes, for every plan, so it reports a line
break in an included file.

```bash
API=http://localhost:3000/api/v1/workflow
echo '{"SITE": "lab"}' > vars.json
curl -sS --fail-with-body -X POST -F 'config=@phenix.yml;type=application/x-yaml' \
  -F 'variables=<vars.json;type=application/json' "$API/apply/helloworld?dryRun=true"
```

A config's `kind` and `metadata.name` can use no placeholder but
`${BRANCH_NAME}` and the variables that the request sends.

| Query parameter | Endpoint | Description |
| :--- | :--- | :--- |
| `dryRun=true` | all | Run every check and change nothing. |
| `pending=<ref>` | apply, dry run only | A config you'll store before the real apply. `<ref>` is the `pending` value its config dry run returned. Repeatable. |
| `expect=<action>` | apply | Return 409 and change nothing when the server plans another action. |
| `tag=<key>=<value>` | apply | A tag to store on the experiment. Repeatable. |
| `force=true` | delete | Stop a running experiment before the delete. Without it, a running experiment gets 409. |
| `injects=<dir>` | delete | The absolute path of the injects directory that you remove after the delete. The default is `<base-dir.injects>/<branch>` of the server. |

This is what the command does, minus the injects, for one topology config:

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

The apply returns its plan:

```json
{"action":"createAndStart","experiment":"helloworld","reason":"","dryRun":false}
```

The delete returns what it did. `experiment` is empty when no experiment
is mapped to the branch, and `injectsUsedBy` names the other experiments with
an injection from `injectsDir`. Keep the directory when that list isn't
empty:

```json
{"branch":"helloworld","dryRun":false,"experiment":"helloworld","stopped":true,"injectsDir":"/phenix/injects/helloworld","injectsUsedBy":[]}
```

It returns 409 and changes nothing when more than one experiment is mapped to
the branch, or when the experiment runs and `force` isn't set.

On the phēnix host, the unix socket needs no authentication:

```bash
curl -sS --unix-socket /tmp/phenix.sock -X POST -H 'Content-Type: application/x-yaml' \
  --data-binary @phenix.yml 'http://unix/api/v1/workflow/apply/helloworld?dryRun=true'
```

## Docker Compose

With Docker Compose, run the command inside the `phenix` container, where the
socket is:

```bash
docker exec phenix phenix workflow apply helloworld -n
docker exec phenix phenix workflow apply /phenix/topologies/helloworld -f
docker exec phenix phenix workflow delete helloworld -f
```

Keep topology directories under `/phenix` (the same path in the container)
and pass an absolute path or a bare name. To change a [setting](#settings),
set its environment variable in the `phenix` service and recreate the
container.

## Settings

Two [settings](settings.md#settings-reference) apply; use absolute paths for
both. `base-dir.injects` (default `<base-dir.phenix>/injects`) is where
injects go and where `phenix workflow delete` removes them, and the server's
value wins unless you give `--base-dir.injects`. `base-dir.topologies` (default `<base-dir.phenix>/topologies`) is where a
`NAME` is looked up.

## Limitations

* There's no web UI for workflow apply or workflow delete. A CI job on another machine can call
  the REST API, but must get the injects onto the phēnix host itself.
* Of the REST API, only the two workflow endpoints take
  [variables](#api). `POST /api/v1/configs`,
  `PUT /api/v1/configs/{kind}/{name}` and the web UI don't.
* An apply dry run can reveal whether a file exists on the server; grant
  `workflow:create` only to trusted roles.
* `phenix workflow delete` finds the other users of an injects directory only
  through the absolute `src` paths of the injections in the experiments'
  topologies. It doesn't find a file that an app or a script reads from the
  directory.
* Don't run `phenix workflow apply` and `phenix workflow delete` for the same
  branch name at the same time. The delete removes the injects directory and
  the staging copies that the apply is writing.

## Troubleshooting

* **Permission denied.** Your user may not have write access to the socket
  or the injects directory. Run as the user that runs `phenix ui` (usually
  root), or start `phenix ui` with `--unix-socket-gid` set to your group.
* **The server doesn't answer.** Check that `phenix ui` is running on the
  `--unix-socket` path (`/tmp/phenix.sock` by default).
* **The server doesn't support workflow dry runs, workflow variables or
  workflow delete.**
  Upgrade the server.
* **A variable is not set.** A file has a `${NAME}` placeholder with no value
  and no default. The error records name the variable, the file and the line.
  Set it with `-e NAME=value` or in an env file, write `${NAME:default}`, or
  write `$${NAME}` to keep the text `${NAME}`. Nothing changed.
* **A variable from the shell is not used.** `sudo` and `docker exec` don't
  pass your shell environment, an env file overrides the shell, and a
  variable whose name starts with `PHENIX_` needs `-e NAME`. Run
  `phenix workflow render` to see the values.
* **The run asks for `-f`.** The plan would restart a running experiment.
  Nothing changed; run it again with `-f`.
* **The plan changed (409).** The experiment was started or stopped between
  the `plan` and `apply` steps. Run it again.
* **The injects destination is refused.** It overlaps a topology directory,
  or the server reports a relative `base-dir.injects`. Check
  `base-dir.injects` and `-b`.
* **A run stopped partway.** The injects and stored configs stay in place,
  and the error says what changed. Fix the cause and run it again. Only a run
  that stops in `apply` can have changed the experiment.
* **Hidden `.<name>.tmp-*` or `.<name>.old-*` entries in
  `<base-dir.injects>`.** An interrupted run left them. Remove them when no
  other run is active, or run `phenix workflow delete <name>`.
* **The delete asks for `-f`.** The experiment mapped to the branch name
  runs. Nothing changed; run it again with `-f` to stop and delete it.
* **The delete kept the injects directory.** Another experiment injects a
  file from it, and the warning names it. Delete that experiment or change its
  injections, then run `phenix workflow delete` again.
* **No `commit` tag.** Git refuses a repository another user owns (common
  under `docker exec`). Add the directory to git's `safe.directory`.
* **No output.** `--log.level` at `warn` or higher hides the plan and result
  of `phenix workflow apply`; rely on the exit status. The `would ...` lines of
  `phenix workflow delete -n` always go to standard output.

## See Also

* [phenix workflow](cli/workflow.md): every flag of the command.
* [Configuration](configuration.md): the config kinds you put in
  `phenix-configs/`.
* [Experiments](experiments.md): managing the experiments a workflow creates.
