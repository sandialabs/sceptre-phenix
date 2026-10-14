# phenix Background Jobs

`phenix ui` runs long operations as background jobs: the request that starts
one answers `202 Accepted` with the job and `Location: /api/v1/jobs/{id}`, and
the job runs on in the server. Jobs live only in that server's memory: a
restart loses them and stops their work. User docs: `docs/content/jobs.md`.
Code: `src/go/api/job` (registry), `src/go/web/jobs.go` (routes).

## Model

- `status`: `queued` -> `running` -> `succeeded` | `partial` (some items
  succeeded) | `failed` (none did, or the job failed; see `error`) |
  `canceled`.
- `items[]`: `{name, status, error, retryable, result}`, status `pending`,
  `running`, `succeeded`, `failed`, `skipped` (e.g. `you don't have
  permission`) or `canceled`. `retryable` marks failures worth retrying later:
  a held VM/experiment lock (`... is locked with status ...`), a timeout.
- `progress`: `{done, total, bytes, bytesTotal}`; `artifact`: `{name, size,
  incomplete}` for a file to download.
- `revision` increases on every change; keep the highest.
- Limits: 4 running jobs per user, 16 in all, the rest `queued` FIFO. A
  finished job is kept 1 h, at most 200; `DELETE` dismisses it at once.

## Routes

| Route | Answers |
|---|---|
| `GET /jobs?experiment=&kind=&active=true` | `{"jobs": [...]}`, newest first, without item `result`s |
| `GET /jobs/{id}` | the job, every item with its result |
| `POST /jobs/{id}/cancel` | `202` + job (`cancelRequested`); `409` finished or not cancelable |
| `DELETE /jobs/{id}` | `204`; `409` while queued or running |
| `GET/HEAD /jobs/{id}/artifact` | the file, resumable with `Range`; `404` when there is none |

The first three are also served on the unix socket (as `global-admin`), which
`phenix job list|get|cancel|wait` uses (see [`cli.md`](cli.md)); the CLI
cannot start jobs.
Websocket (`/ws`) messages: `{"resource": {"type": "job", "name": <id>,
"action": "update"|"remove"}, "result": <job>}`, throttled to 4/s per job
while running, without item `result`s and with at most 500 items
(`itemsOmitted` counts the rest).

## Kinds

| Kind | Started by | Items | Cancel |
|---|---|---|---|
| `vms.action` | `POST /experiments/{exp}/vms/actions` (`{"action", "vms"}`; each VM checked as its single-VM route: `vms/start`, `vms/stop` (pause), `vms/restart`, `vms/shutdown`, `vms/reset` (resetDisk) `update`, or `vms` `delete` (kill); 403 if none allowed) | the VMs; `result` is the VM | stops dispatching; the 4 VMs in progress finish. Forbidden VMs `skipped`, locked VMs `failed` + `retryable` |
| `files.archive` | `POST /experiments/{name}/files/archive` (`{"paths"}`; checks and limits as `/files/download`; `experiments/files` `get`) | the files | stops at once; no archive. Artifact `<exp>-files.zip`, or `<exp>-files-incomplete.zip` (`artifact.incomplete`, job `partial`) with `MISSING.txt` listing files left out. Logs `archived file` when built and `downloaded file` on the artifact's first GET |
| `disk.clone` | `POST /disks/clone/jobs?disk=&new=` (checks as `/disks/clone`; `disks` `create` on the new name) | the new image's file name | stops at once; the hidden `.<new>.partial-<hex>` copy is removed (and swept on startup) |

## RBAC

A job belongs to the user who started it. Other users need resource `jobs`
with `list`/`get`/`update`/`delete` (see / download, cancel, dismiss) on the
job's experiment as resource name; a job of no experiment (disk clone) needs
`*`. Everyone else gets `404`. Experiment Admin has `jobs` on its experiments
(added to stored roles on startup); Global Admin matches through `*`.
Starting a job needs only the operation's own permission.
