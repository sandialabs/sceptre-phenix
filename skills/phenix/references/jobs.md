# phenix Background Jobs

`phenix ui` runs long operations as background jobs, which run on in the
server after the request that starts them answers. Jobs live in the server's
memory; a restart loses them. See [`cli.md`](cli.md) for shutdown
behavior.
User docs: `docs/content/jobs.md`.
Code: `src/go/api/job` (registry), `src/go/web/jobs.go` (routes).

## Model

- `status`: `queued` -> `running` -> `succeeded` | `partial` (some items
  succeeded) | `failed` (none did, or the job failed; see `error`) |
  `canceled`.
- `items[]`: `{name, status, error, retryable, result}`, status `pending`,
  `running`, `succeeded`, `failed`, `skipped` (e.g. `you do not have
  permission`) or `canceled`. `retryable` marks failures worth retrying later,
  such as a held lock or a timeout.
- `progress`: `{done, total, bytes, bytesTotal}`; `artifact`: `{name, size,
  incomplete}` for a file to download.
- `revision` increases on every change; keep the highest.
- Limits: 4 running jobs per user, 16 in all, the rest `queued` FIFO; at
  most 32 queued per user and 256 in all, beyond which starting a job that
  would queue answers `429` with `Retry-After`. A
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

## RBAC

A job belongs to the user who started it. Other users need resource `jobs`
with `list`/`get`/`update`/`delete` (see / download, cancel, dismiss) on the
job's experiment as resource name; a job of no experiment needs
resource `jobs/global` with the same verbs instead, which no `jobs` resource
name reaches, not even `*`. Everyone else gets `404`. Experiment Admin has
`jobs` on its experiments (added to stored roles on startup); Global Admin
matches both through `*` and `*/*`.
Starting a job needs only the operation's own permission.
