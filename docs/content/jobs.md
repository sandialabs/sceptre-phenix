# Background Jobs

Some operations take longer than a web request should: acting on many VMs at
once, building a zip archive of experiment files, or cloning a large disk
image. phēnix runs these as **background jobs** on the server. The request
that starts one answers at once with the job, and the job then runs on its
own: closing the browser tab, reloading the page or losing the connection does
not stop it, and the UI picks it up again when you come back.

Each job records:

* what it acts on, as a list of **items** (VMs, files or a disk), each with
  its own outcome;
* its **progress**, as items done out of the total and, for file work, bytes;
* an **artifact**, a file the job produced for you to download, if any.

## What runs as a job

| Kind | Started by | Items |
|---|---|---|
| `vms.action` | the VM table's bulk actions (`POST /api/v1/experiments/{exp}/vms/actions`, see [VM Multi Action](vm-multi-action.md#from-the-api)) | the VMs |
| `files.archive` | downloading several experiment files (`POST /api/v1/experiments/{name}/files/archive`, see [Experiments](experiments.md#acting-on-several-files)) | the files |
| `disk.clone` | cloning a disk image (`POST /api/v1/disks/clone/jobs`, see [Disks](disks.md#cloning-in-the-background)) | the new image |

Other operations, such as VM snapshots, commits and memory snapshots, experiment
starts and SCORCH runs, still run within their request and are not jobs.

## Lifecycle

A job is `queued` until a slot frees, then `running`, then ends in one of
four statuses:

| Status | Meaning |
|---|---|
| `succeeded` | every item succeeded |
| `partial` | some items succeeded and others failed or were skipped |
| `failed` | no item succeeded, or the job itself failed; `error` says why |
| `canceled` | someone canceled the job before it finished |

phēnix runs at most 4 jobs at once for each user and 16 in all; later jobs
wait, `queued`, and start in the order they came as slots free.

### Item outcomes

Each item ends as `succeeded`, `failed`, `skipped` or `canceled`. A failed or
skipped item carries an `error` saying why: a VM your role may not act on is
skipped with `you don't have permission`, for example, while the rest of the
job goes on.

A failed item is marked `retryable` when trying it again later may succeed,
such as a VM busy with another action (`vm ... is locked with status ...`) or
a request that timed out. The web UI offers **Retry failed** for these items;
retrying starts a new job with just them.

### Canceling

A job you can cancel answers `POST /jobs/{id}/cancel` with `202` and
`cancelRequested: true`, and ends `canceled`. A queued job is canceled at
once. A running job stops when its work notices:

* a VM action job starts no more VMs, and finishes the minimega commands
  already running, so it stops after the VMs in progress finish;
* an archive or clone job stops at once and leaves no file behind.

Items that succeeded before the cancel keep their outcome; the others are
`canceled`.

## Retention

Jobs live **in the memory of the `phenix ui` server only**:

* a finished job is kept for one hour, and at most 200 finished jobs are kept
  (the oldest go first); dismissing a job forgets it at once;
* restarting the server loses every job, and stops the work of the running
  ones;
* the files a job leaves when the server stops mid-way are deleted when it
  starts again (`<base-dir.phenix>/jobs/` and partial disk clones).

Download an archive soon after it is built: it is deleted with its job.

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

The permission's resource name is the job's experiment, so an Experiment Admin
role sees and cancels the jobs of its own experiments. A job of no experiment,
such as a disk clone, needs the permission on every resource (`*`), which only
Global Admin has. Starting a job needs only the permission of the operation
itself, never `jobs`.

To anyone else, another user's job answers `404`, as if it did not exist.

## Command line

`phenix job` follows and cancels the jobs of the running `phenix ui` server
over its unix socket (`--unix-socket`), which acts as Global Admin, so it sees
every user's jobs:

```bash
phenix job list                          # every job, newest first
phenix job list --experiment exp1 --active -o json
phenix job get j0f3a9c2e81b4d7a6         # the job and each item's outcome
phenix job cancel j0f3a9c2e81b4d7a6
phenix job wait j0f3a9c2e81b4d7a6 --timeout 10m
```

`phenix job wait` asks for the job every second until it finishes, prints it,
and exits `0` when it succeeded, `2` when it ended `partial`, and `1` when it
failed or was canceled, or when waiting timed out. Without a running server,
each command fails with `phenix ui is not running (no socket at ...)`.

!!! note
    The command line can follow jobs, but not start them. Commands such as
    `phenix vm start` act in the `phenix` process itself rather than through the
    server, so they wait for their result as before and are not jobs; start
    bulk VM actions, file archives and disk clones from the web UI or the REST
    API.

## REST API

| Route | Does |
|---|---|
| `GET /api/v1/jobs` | lists your jobs, and the ones you may list, newest first; `?experiment=`, `?kind=` and `?active=true` filter it |
| `GET /api/v1/jobs/{id}` | returns one job, with every item and its result |
| `POST /api/v1/jobs/{id}/cancel` | cancels it: `202`, or `409` once it finished or when it cannot be canceled |
| `DELETE /api/v1/jobs/{id}` | dismisses a finished job and deletes its artifact: `204`, or `409` while it runs |
| `GET /api/v1/jobs/{id}/artifact` | downloads its artifact, resumable with `Range` (see [API](api.md#downloads)) |

A route that starts a job answers `202 Accepted` with the job and a
`Location: /api/v1/jobs/{id}` header. It checks what it can first, such as
permissions and limits, and answers with the usual `400`, `403`, `404`, `409`
or `413` without starting a job when they fail.

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
  "params": {"action": "restart"},
  "status": "partial",
  "cancelable": true,
  "progress": {"done": 3, "total": 3},
  "items": [
    {"name": "web", "status": "succeeded", "result": {"name": "web", "running": true}},
    {"name": "db", "status": "failed", "error": "vm db is locked with status stopping", "retryable": true},
    {"name": "fw", "status": "skipped", "error": "you don't have permission"}
  ],
  "created": "2026-10-14T15:04:05Z",
  "updated": "2026-10-14T15:04:09Z",
  "started": "2026-10-14T15:04:05Z",
  "finished": "2026-10-14T15:04:09Z",
  "revision": 9
}
```

`revision` grows with every change, so a client that gets updates out of order
keeps the one with the highest revision.

### Websocket updates

The web UI's websocket (`GET /api/v1/ws`) sends each change to a job, at most
four times a second while it makes progress and at once when its status
changes, to the job's owner and to the users who may list it:

```json
{"resource": {"type": "job", "name": "j0f3a9c2e81b4d7a6", "action": "update"}, "result": {...}}
```

`action` is `remove` when the job is dismissed or forgotten. These messages
leave out the items' `result`, and carry at most 500 items, with
`itemsOmitted` counting the rest; `GET /api/v1/jobs/{id}` has them all.
