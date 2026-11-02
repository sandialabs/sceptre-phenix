# Background Jobs

Some operations take longer than a web request should: acting on many VMs at
once, building a zip archive of experiment files, or cloning a large disk
image. phēnix runs these as **background jobs** on the server. The request
that starts one answers at once, and the job then runs on its own, even if
you close the browser tab or lose the connection.

Each job records the **items** it acts on (VMs, files or a disk), each with
its own outcome, its **progress**, and an **artifact**, a file it produced
for you to download, if any.

## What runs as a job

| Kind | Started by | Items |
|---|---|---|
| `vms.action` | the VM table's bulk actions (see [VM Multi Action](vm-multi-action.md#from-the-api)) | the VMs |
| `files.archive` | downloading several experiment files (see [Experiment Files](experiment-files.md#acting-on-several-files)) | the files |
| `disk.clone` | cloning a disk image (see [Disks](disks.md#cloning-in-the-background)) | the new image |
| `experiments.start` | a start of `all` experiments through the API (`POST /api/v1/experiments/all/start`) | the experiments |
| `experiments.trigger` | a trigger of `all` experiments through the API (`POST /api/v1/experiments/all/trigger`) | the experiments |
| `experiments.periodic` | a start of an experiment through `phenix ui` when its scenario has an app with `runPeriodically` (see [Starting / Stopping Experiments](experiments.md#starting-stopping-experiments)) | none |

An `experiments.periodic` job runs the `running` stage of the apps on their
schedule until the experiment stops or someone cancels the job. An experiment
has at most one such job. The job does not use one of the slots that limit
the running jobs.

Other operations, such as VM snapshots, the start of one experiment and SCORCH
runs, run within their request and are not jobs.

## Lifecycle

A job is `queued` until a slot frees, then `running`, then ends in one of
four statuses:

| Status | Meaning |
|---|---|
| `succeeded` | every item succeeded |
| `partial` | some items succeeded and others failed or were skipped |
| `failed` | no item succeeded, or the job itself failed, and `error` says why |
| `canceled` | someone canceled the job before it finished |

phēnix runs a few jobs at once for each user and queues the rest. A request
that would overfill the queue answers `429 Too Many Requests` with
`Retry-After`.

### Item outcomes

Each item ends as `succeeded`, `failed`, `skipped` or `canceled`. A failed or
skipped item carries an `error` saying why, such as `you do not have
permission` for a VM your role may not act on. A failed item is marked
`retryable` when trying it again may succeed, and the web UI offers
`Retry failed` for these items.

### Canceling

A queued job is canceled at once. A running VM action job starts no more VMs,
and stops after the VMs in progress finish. A running archive or clone job
stops at once and leaves no file behind.

## Retention

Jobs live in the memory of the `phenix ui` server only. A finished job is
kept for one hour, or until you dismiss it, and its artifact is deleted with
it. Restarting the server loses every job and cancels the running ones (see
[Stopping the Server](settings.md#stopping-the-server)).

## Who sees a job

A job belongs to the user who started it. Other users see it, cancel it or
dismiss it only with the `jobs` permission (see
[User Administration](user-administration.md#resources)):

| Verb | Lets a role, for other users' jobs |
|---|---|
| `list` | list them, and get their websocket updates |
| `get` | read them and download their artifacts |
| `update` | cancel them |
| `delete` | dismiss them |

The permission's resource name is the job's experiment, so an
`Experiment Admin` sees and cancels the jobs of its own experiments. A job of
no experiment, such as a disk clone, needs the same verbs on `jobs/global`
instead. The `experiments.start` and `experiments.trigger` jobs also have no
experiment. Starting a job needs only the permission of the operation itself. To
anyone else, another user's job answers `404`.

## Web UI

The [background jobs menu](web-ui.md#background-jobs-menu) in the header
lists your jobs with their progress, and cancels, downloads and dismisses
them. Its button turns red or green when jobs have failed or succeeded since
you last opened it.

## Command line

`phenix job` follows and cancels the jobs of the running `phenix ui` server
over its unix socket, which acts as `Global Admin`, so it sees every user's
jobs:

```bash
phenix job list                          # every job, newest first
phenix job list --experiment exp1 --active -o json
phenix job get j0f3a9c2e81b4d7a6 -o yaml # the job and each item's outcome
phenix job cancel j0f3a9c2e81b4d7a6
phenix job wait j0f3a9c2e81b4d7a6 --timeout 10m
```

`phenix job wait` waits until the job finishes, prints it, and exits `0` when
it succeeded, `2` when it ended `partial`, and `1` otherwise. Without a
running server, each command fails with `phenix ui is not running`. `-o`
takes `table`, `json` or `yaml`. JSON and YAML have the field names of the
REST API. `phenix job cancel --dry-run` gets the job and does not cancel it.

!!! note
    The command line can follow jobs, but not start them. Commands such as
    `phenix vm restart` act in the `phenix` process itself and wait for their
    result. Start bulk VM actions, file archives and disk clones from the web
    UI or the REST API.

## REST API

| Route | Does |
|---|---|
| `GET /api/v1/jobs` | lists the jobs you may see, newest first, without their items' results. `?experiment=`, `?kind=` and `?active=true` filter it. An unknown `kind`, or an `active` that is not `true` or `false`, gives `400` |
| `GET /api/v1/jobs/{id}` | returns one job, with every item and its result |
| `POST /api/v1/jobs/{id}/cancel` | cancels it: `202`, or `409` once it finished |
| `DELETE /api/v1/jobs/{id}` | dismisses a finished job and deletes its artifact: `204`, or `409` while it runs |
| `GET /api/v1/jobs/{id}/artifact` | downloads its artifact, resumable with `Range` (see [Downloads](api.md#downloads)) |

A route that starts a job checks permissions and limits first, then answers
`202 Accepted` with the job and a `Location: /api/v1/jobs/{id}` header.

```bash
# list your running jobs
curl -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  "http://localhost:3000/api/v1/jobs?active=true"

# follow one
curl -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  http://localhost:3000/api/v1/jobs/j0f3a9c2e81b4d7a6

# cancel it
curl -X POST -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  http://localhost:3000/api/v1/jobs/j0f3a9c2e81b4d7a6/cancel
```

A job looks like this:

```json
{
  "id": "j0f3a9c2e81b4d7a6",
  "kind": "vms.action",
  "owner": "alice",
  "experiment": "exp1",
  "title": "Restart 3 VMs",
  "status": "partial",
  "progress": {"done": 3, "total": 3},
  "items": [
    {"name": "web", "status": "succeeded"},
    {"name": "db", "status": "failed", "error": "vm db is locked with status stopping", "retryable": true},
    {"name": "fw", "status": "skipped", "error": "you do not have permission"}
  ],
  "revision": 9
}
```

`revision` grows with every change, so a client that gets updates out of
order keeps the one with the highest revision.

### Retrying a request that starts a job

A client that loses the `202` answer cannot tell whether the job started. To
retry safely, send an `Idempotency-Key` header (any 1 to 255 printable ASCII
characters, such as a UUID) with the request, and the same key with each
retry. A retry with the same key, body and query by the same user gets the
first answer again, with `Idempotent-Replayed: true`, and starts no second
job. The same key with a different body or query is refused with `422`. Keys
are kept for an hour, and a server restart forgets them.

```bash
# one key per request, reused for each of its retries
KEY=$(uuidgen)
curl -X POST -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  -H "Idempotency-Key: $KEY" \
  -d '{"action": "restart", "vms": ["web", "db"]}' \
  http://localhost:3000/api/v1/experiments/exp1/vms/actions
```

### Websocket updates

The web UI's websocket (`GET /api/v1/ws`) sends each change to a job to its
owner and to the users who may list it, as a `job` resource message whose
`action` is `update`, or `remove` when the job is dismissed. These messages
leave out the items' results, which `GET /api/v1/jobs/{id}` has.
