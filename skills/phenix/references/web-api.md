# phenix Web API Reference

`phenix ui` serves the web UI and REST API together on the same port —
`0.0.0.0:3000` by default (`ui.listen-endpoint` / `--listen-endpoint`,
`PHENIX_UI_LISTEN_ENDPOINT`). All routes below are relative to the `/api/v1`
base path unless noted.

Prefer the equivalent `phenix` CLI command over calling the web API directly
unless the HTTP interface is specifically needed (e.g. scripting against a
running `phenix ui` server, or building a UI integration).

When phenix runs in its Docker container, the bundled compose file uses
`network_mode: host`, so `localhost:3000` on the host reaches it directly
with no port mapping.

## Authentication

Authentication depends on `phenix ui --jwt-signing-key` (`ui.jwt-signing-key`,
`PHENIX_UI_JWT_SIGNING_KEY`):

- **No signing key (the default, including the bundled Docker compose file):**
  auth is disabled. Every request is served as the built-in `global-admin` role
  and no token is required or checked. The server logs
  `no JWT signing key provided -- disabling auth` at startup.
- **Signing key set:** a JWT is required. Obtain one from `/login` (registered
  for both `GET` with HTTP basic auth and `POST` with a JSON body) and send it
  on every request in the `X-Phenix-Auth-Token` header as `Bearer <jwt>` — NOT
  the standard `Authorization` header, which is left free for proxy
  authentication. The token
  may also be passed as a `?token=<jwt>` query parameter, which is how browser
  links and image tags reach `/screenshot.png` and `/vnc/ws`.
- `--jwt-signing-key proxy-jwt` accepts JWTs minted by an upstream proxy, and a
  `dev|<user>|<role>` key enables development auth, treating every request as
  that user and role; any other `dev|` key answers every request with `500`.

With auth on, a request without a token gets `403`; a malformed, expired or
signed-out token, or a `--proxy-auth-header` user other than the token's,
`401`. A missing permission is `403`, but jobs, consoles and capture tokens of
other users answer `404`. Unknown `/api/v1` paths answer `404` and wrong methods
`405`, before auth; `OPTIONS` answers `204`, also before auth, without
running the route. Under `proxy-jwt` the token is read only from the header, and
a missing or malformed one is `400`. Behind a proxy with `--base-path` the proxy
strips it, and job and config `Location` headers include it.
`src/go/web/contract_test.go` pins these per route.

Every error, auth, RBAC and routing refusals included, is the JSON body
`{"message", "cause", "metadata"?, "details"?, "code"}` (WebShark's own `/webshark/`
routes, the health routes and a failed console, VNC, port forward or SCORCH
websocket handshake aside); match on
`code` (`invalid_request`, `unauthorized`, `forbidden`, `not_found`,
`conflict`, `rate_limited`, `unavailable`, `internal`, `broken_disks`), not
the message. `broken_disks` is a 400 from a start (`POST
/experiments/{name}/start`, a workflow apply that starts or restarts) refused
because a VM disk image or CD-ROM image is missing or its backing chain is
broken; `details.disks` lists `{vm, drive, image, path, missing, problem}`
per disk, with full paths. A bulk `vms.action` start or restart checks only
VMs that minimega launches again (state `QUIT`, `ERROR` or unknown), and fails
each such VM's item with `broken VM disk images: ...` instead. An
unexpected failure is `500` with `internal server error`, its cause only in
the server log.

```bash
TOKEN=$(curl -s -u admin:password http://localhost:3000/api/v1/login | jq -r .token)
curl -H "X-Phenix-Auth-Token: Bearer $TOKEN" http://localhost:3000/api/v1/experiments
```

Sign-in and sign-out, user, token, config and experiment changes, workflow
applies and config upserts, settings changes, app
triggers, SCORCH runs, VM power actions, VM PATCHes, optical disc changes,
netflow captures and their downloads, and the capture, capture token, WebShark, experiment file,
minimega console, VNC websocket, port forward websocket, disk, VM snapshot,
commit and mount routes log `AUDIT` events with ECS fields (`event.action`,
`event.outcome`, `user.name`, `source.ip`, ...) for successes, failures and
RBAC refusals, always written to the system log file whatever `log.level` is;
sign-in and sign-out log no `SECURITY` records. A request refused for its
token (missing, malformed, invalid, expired, revoked, ...) is a
`request-auth` failure, at most one a minute for each client address and
reason (`phenix.suppressed` counts the rest), never with the token; a
refused proxy JWT's unverified user is `phenix.auth.claimed_user`, not
`user.name`. Workflow
dry runs carry `phenix.dry_run` and `event.type` `info`, and requests over
the unix socket have no `user.name` or `source.ip` but `phenix.auth.method`
`unix-socket`. See `docs/content/logging.md` ("Audit Events") for the actions
and fields. A disk clone job and a bulk VM action job are audited when they
finish, as the user who started them, and an archive job's files when
`GET /jobs/{id}/artifact` downloads the archive.

## Routes

`src/go/web/public/docs/openapi.yml`, served rendered at `/docs/`, is the route
reference: every route `src/go/web/server.go` serves, websockets and routes
outside `/api/v1` included, with parameters, bodies, permissions and status
codes. `TestRouterMatchesOpenAPI` keeps the two in step, so read the spec
rather than guessing a path. The server also serves the raw spec at
`/docs/openapi.yml`, with `info.version` set to its build, and every `/api/v1`
response past authentication names that build in `X-Phenix-Version` (an
authentication refusal, and a `404` or `405` for an unserved path or method,
lack it). Points that are easy to miss:

- Outside `/api/v1`, at the server root: `/healthz` and `/readyz` (no token;
  see `docs/content/health.md`), `/version`, `/features`,
  `/downloads/tunneler` (with the `tunneler-download` feature) and the
  `/webshark/` routes, including `GET /webshark/streams/{token}`, whose `pcs_`
  stream token is its only credential.
- Feature-gated routes: the VM mount routes need the `vm-mount` feature, the
  `/console` routes `phenix ui --minimega-console`, and WebShark's view routes
  an installed WebShark.
- Unmatched `/api/v1/*` requests return a JSON `404`; only non-API routes fall
  through to the SPA index. A route behind a disabled feature flag is therefore
  a real `404`, not `200 text/html`.
- VM power changes are `POST`: `.../vms/{name}/start`, `/stop`, `/restart`,
  `/shutdown`, `/reset` and `/redeploy`, or `POST /experiments/{exp}/vms/actions`
  for several VMs at once. `PATCH /experiments/{exp}/vms` changes several VMs'
  settings, carrying on past a failure, and lists each VM it skipped or failed
  in the response's `errors` (`403` if the role may patch none, `500` if all
  fail). Sign out with `POST /logout`.
- `POST /experiments/{name}/stop` that cannot kill the VMs leaves the
  experiment running and answers 502 with `metadata.retry` `force`;
  `?force=true` marks it stopped anyway.
- Paths use camelCase (`captureSubnet`, `memorySnapshot`, `captureTokens`).
- `GET /schemas/{version}` returns the whole schema file (`v0` Workflow, `v1`
  Topology/Experiment/Image/User/Role, `v2` Scenario).
  `GET /schemas/{kind}/{version}` returns one kind, only at the version it is
  stored at; any other kind or version answers `404`.
- Bulk VM actions, `POST /disks/clone/jobs` and `POST .../files/archive` start
  [background jobs](jobs.md) and answer `202` with a `Location`.
- Workflow routes are covered in [`workflow.md`](workflow.md#rest-endpoints).
- `GET /disks` marks an image qemu cannot open (a missing backing file, a
  loop, or more than 64 images) with `brokenChain`, `chainProblem` and
  `missingBacking` (full path; empty for a loop); `backingImages` then holds
  only the readable part. phenix reads the head node's files; no CLI command
  lists these fields.
- `GET /experiments`, `/vms`, `/disks`, `/jobs` and the experiment VM and
  file lists page with `pageNum` and `perPage` (both, from 1; `400`
  otherwise) and report the unpaged `total`; without them they return
  everything.
- `GET /experiments/{name}` lists only VMs carrying any repeated
  `label=key=value` (`400` without an `=` or a key) and running on any repeated
  `host=<host>` (`400` when empty); it and `/experiments/{name}/vms`
  sort by label count with `sortCol=labels`, by VLAN IDs (as numbers) or tap
  names in interface order with `sortCol=network` or `taps` (VMs with none
  last, ties by name), and the VM list's `labels` and `hosts` hold every label
  and cluster host to filter by and its `running` counts the running VMs, all
  before filtering and paging. The websocket `experiment/vms` `list` request
  takes the same filters as `labels` and `hosts`.
- Slow `/ws` clients are closed with code 1013 and should reconnect; the live
  log stream may skip lines under load.

When a config fails its schema, the config routes (`POST /configs`,
`PUT /configs/{kind}/{name}`) and the workflow routes answer 400 with the
explained lines in `metadata.validation`, one per line, and the validator's
text in `metadata.validation-raw`; see [`workflow.md`](workflow.md).

`POST /users` and `POST /signup` answer `409` `user <name> already exists` for
a name another user has, and `500` `error creating user` for other failures.
`POST /users` answers `400` `role not found` for an unknown role before it
creates anything.
Both answer `400` `invalid username ...` for a username that does not start
with a letter or digit, or that has a character other than letters, digits,
`.`, `_`, `@`, `+` and `-`, and create no user.

Config revisions: `GET /configs/{kind}/{name}` sends `ETag: "r<revision>"`
and `X-Phenix-Revision` (none for a config with no stored revision; the ETag stays strong when gzipped) and answers `If-None-Match` with
`304`. `PUT` and `DELETE` take an optional `If-Match` (`"r<n>"` list or `*`):
`412` with `metadata.revision` when the config moved on, `400` when malformed.
An unknown config is `404` (a `PUT` or `DELETE` with `If-Match`: `412`;
`GET` ignores `If-Match`).
`POST` and `PUT` return the new `ETag`. `GET /configs` returns `[]`, never
`null`.

Config RBAC names are `Kind/name` (`Topology/demo`). `POST /configs` and the
create branch of `POST /workflow/configs/{branch}` check `configs` `create`
against the new config's `Kind/name`, and a `PUT` that renames a config or
changes its kind also needs `update` and `create` for the new one; otherwise
`403`.
A `User` config comes back from `GET /configs/{kind}/{name}` and
`POST /configs/download` without `password` and `tokens` (the ETag is still the
stored revision), and `PUT` or the workflow upsert keeps the stored ones,
ignoring any in the body.

Ordering: every `/api/v1` route's response past auth (not a `401`/`403` auth
refusal or an `OPTIONS` answer) has `X-Phenix-Seq` (taken as the request
started) and every `/ws` message a `seq` (taken as it was published; a VM list
reply's before its minimega read), from one strictly increasing sequence per
`phenix ui` process. Data tagged S reflects every change whose message has a
seq of S or less; keep the highest seq applied per item, ignore lower, and
reset on websocket reconnect. Runtime reads add `X-Phenix-Observed-At` (when
minimega was read; compare with `Date`) and `X-Phenix-Stale: true` (earlier
data while minimega is busy). A minimega failure is never an empty success:
VM lists set `runtime_unknown` (`vms_unknown` per experiment) and list
unreported VMs as configured, `/soh` has `vms.known`/`vms_stale`, file listings
answer `502`, and `GET /experiments` answers `500` when the store is
unreadable (and is `X-Phenix-Stale` when it leaves out unreadable experiments). See `docs/content/api.md#freshness`.

## Downloads

Single-file downloads stream from disk and resume:
`GET /experiments/{name}/files/{filename}?path=`, `GET /disks/download?disk=`,
the VM mount's `GET .../files/download?path=`, `GET /downloads/tunneler/{name}`,
a saved capture's `GET /webshark/captures/{name}`, a recorded netflow
capture's `GET /experiments/{name}/netflow/captures/{id}?format=text` and the
log files' `GET /logs/files/{phenix|minimega}` (`logs` `get`; `GET /logs/files`
says which can be downloaded and why not; audited as `log-download`). Each also answers `HEAD`,
and sends `Accept-Ranges: bytes`, `Content-Length`, a
strong `ETag` (from the file's inode, size and nanosecond modification time,
never its contents) and `Last-Modified`, with `Cache-Control: private,
no-cache`. `Range` gives a `206` (or `416` with `Content-Range: bytes */<size>`),
and `If-Range` with the `ETag` resumes only while the file is unchanged,
answering a changed file with the whole new one (`200`). `If-None-Match` gives
`304`. Downloads are attachments and are never gzip-compressed; a compressed
API response drops `Accept-Ranges` and `Repr-Digest` and weakens its `ETag` to
`W/"..."` (except a config's revision ETag).

With `Accept: text/plain` the same route is the Files tab's viewer: inline,
`text/plain; charset=utf-8`, compressible, and `413` for a file over 10 MiB.
The `file-download` audit event fires once per download (a `GET` answered
`200`, or `206` from byte 0), with the `Range` asked for as
`http.request.range`.

`POST /experiments/{name}/files/download` and `POST /configs/download` build a
zip as they send it, and `GET /experiments/{name}/netflow/captures/{id}` with
`format=csv` or `format=jsonl` converts the capture as it sends it:
`Accept-Ranges: none`, `Cache-Control: no-store`, and no resume. List the
recorded netflow captures with `GET /experiments/{name}/netflow/captures`; a
capture over `ui.netflow.max-size` (MiB, `phenix ui` restart) is `truncated`. Download very large files one at a time.

```bash
curl -C - -o out.pcap -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  "http://localhost:3000/api/v1/experiments/exp/files/out.pcap?path=scorch/run-0/out.pcap"
```

Each time a SCORCH run finishes it archives `scorch/run-<run>/` to
`scorch-run-<run>_<start, 2006-01-02T15-04-05Z0700>.tgz` at the top of the
experiment's files, one archive per execution; the SCORCH runs page's
`Download` menu lists them from `GET /experiments/{name}/files` and downloads
them with this route.

`curl -C -` sends only `Range`, not `If-Range`, so it splices a file that
changed between attempts, such as a disk image a running VM writes to.
Verify with a checksum: `GET /experiments/{name}/files/{filename}/checksum?path=`
and `GET /disks/checksum?disk=` (same permission as the download) answer `202`
with `Retry-After` while the SHA-256 is computed in the background (two files at
a time server-wide, kept in memory while the file is unchanged), then `200`
with `{path, size, modified, etag, sha256}`; `409` if the file changed while
hashed. Once known, downloads of the file also send it as `Repr-Digest:
sha-256=:<base64>:` (RFC 9530), valid for ranges too.

A running capture in WebShark (`/webshark/captures/{name}` while live) sends no
`ETag` or `Last-Modified` and `Cache-Control: no-store`: a range is still the
bytes captured so far, but a resume with `If-Range` gets the whole capture
again. `GET .../captures/{iface}/stream` is a live stream with no ranges.

## minimega Console

The console routes need `phenix ui --minimega-console`. `POST /console` starts
`phenix mm --attach` on a pty and answers `{"pid": N}`; the console belongs to
the user who started it, and every other console route answers 404 for another
user's pid. `GET /console/{pid}/ws` sends up to 256 KiB of recent output, then
live output, as binary frames, and what the client sends is the console's
input. Several websockets can attach at once; closing one leaves the console
running, so the UI keeps it across page changes, reloads and tabs. A console
ends when its process exits (the server then closes its websockets with code
1000), on `DELETE /console/{pid}` (the UI sends it on logout), or once no
websocket has been attached for 15 seconds. `GET /console/{pid}` checks that it
is still running. Starting, resizing and ending need `miniconsole` `post`;
checking and attaching need `miniconsole` `get`.

## Creating an experiment

`POST /experiments` takes JSON and answers `204 No Content`; websocket clients
then get an `experiment` `create` event carrying the new experiment.

```json
{
  "name": "exp1",
  "topology": "example",
  "scenario": "example",
  "disabled_apps": ["soh"],
  "vlan_min": 100,
  "vlan_max": 200,
  "deploy_mode": "no-headnode",
  "default_bridge": "exp1",
  "workflow_branch": "main",
  "annotations": {"phenix.workflow/tags": "run=nightly"},
  "node_annotations": {
    "phenix/default-apps": false,
    "phenix/startup-autotunnel": ["8080:80"]
  }
}
```

Only `name` and `topology` are required. `annotations` become the experiment's
metadata annotations: `topology` and `scenario` are set by phenix and rejected,
and `phenix.workflow/branch` comes from `workflow_branch`, so a request may not
give both. `node_annotations` are added to every VM in the experiment's own
copy of the topology (the Topology config is unchanged), keep their JSON types,
and never replace a VM's own value for the same key. A value of the wrong type
for an annotation the default apps read (see [`annotations.md`](annotations.md))
is rejected with `400` naming the key. Giving `phenix/startup-autotunnel` in
`node_annotations` needs `vms/forwards` `create` on every VM the experiment
deploys (external nodes excluded), or the request gets `403`, because the
startup app creates those forwards.

## VM OS type

Every VM object the REST API and websocket VM updates send carries `os_type`,
its topology node's `hardware.os_type` (`linux`, `windows`, `vyos`, ...), left
out when the node gives none.

## VM annotations

`GET /experiments/{exp}/vms/{name}` includes the VM's own `annotations` from the
experiment's copy of the topology; VM lists and websocket VM updates carry
`null` instead. `PATCH /experiments/{exp}/vms/{name}` with
`{"annotations": {...}}` replaces the whole set (`{}` removes them all), also
while the experiment is running, where only `interface` (the VLAN it connects
to), `tags` and `annotations` may change. Values keep their JSON types, and the
same type checks as creation reject a bad value with `400` naming the key. The
response carries the saved annotations only when the role may also `get` the
VM. Setting `phenix/startup-autotunnel` to a value the VM does not already have
needs `vms/forwards` `create` on that VM, or the request gets `403`.

```json
{"annotations": {"phenix/startup-autotunnel": ["8080:80"], "vncBanner": "lab"}}
```
