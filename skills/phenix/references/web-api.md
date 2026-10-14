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
  `dev|`-prefixed key enables development auth.

```bash
TOKEN=$(curl -s -u admin:password http://localhost:3000/api/v1/login | jq -r .token)
curl -H "X-Phenix-Auth-Token: Bearer $TOKEN" http://localhost:3000/api/v1/experiments
```

The capture, capture token, WebShark, experiment file, minimega console, VNC
websocket, port forward websocket, disk, VM snapshot, commit and mount routes
log `AUDIT` events with ECS fields (`event.action`, `event.outcome`,
`user.name`, `source.ip`, ...) for successes, failures and RBAC refusals,
always written to the system log file whatever `log.level` is; see
`docs/content/logging.md` ("Audit Events") for the actions and fields. A disk
clone job is audited when it finishes, as the user who started it, and an
archive job's files when `GET /jobs/{id}/artifact` downloads the archive.

## Routes

| Resource | Routes |
|---|---|
| Configs | `GET/POST /configs`, `GET/PUT/DELETE /configs/{kind}/{name}`, `POST /configs/download` |
| Schemas | `GET /schemas/{version}`, `GET /schemas/{kind}/{version}` |
| Experiments | `GET/POST /experiments`, `GET /experiments/{name}`, `PATCH /experiments/{name}`, `DELETE /experiments/{name}`, `POST /experiments/{name}/start`, `POST /experiments/{name}/stop`, `GET /experiments/{name}/apps`, `POST/PUT /experiments/builder` |
| Experiment detail | `GET /experiments/{name}/topology`, `GET /experiments/{name}/topology/search`, `POST/DELETE /experiments/{name}/trigger`, `GET/POST /experiments/{name}/schedule`, `GET /experiments/{name}/soh` (state of health), `GET /soh` (state of health summary for every experiment), `GET /experiments/{name}/captures`, `GET /experiments/{name}/files`, `GET/HEAD/DELETE /experiments/{name}/files/{filename}?path=` (resumable; see [Downloads](#downloads)), `GET .../files/{filename}/checksum?path=`, `POST /experiments/{name}/files/download` and `/files/delete` (`{"paths": [...]}`; zip download, not resumable, and bulk delete), `POST /experiments/{name}/files/archive` (same body and limits; builds the zip in a `files.archive` [background job](jobs.md), leaving out files it cannot fetch, listed in `MISSING.txt`, as `<exp>-files-incomplete.zip`; download from `/jobs/{id}/artifact`), `POST /experiments/{name}/webshark` (`{"path"}` or `{"vm", "interface"}`; readies a capture for WebShark, served at `/webshark/` outside `/api/v1` when installed, and returns a capture name bound to the caller's session: 2 h idle, at most 12 h), `POST/GET /experiments/{name}/capture-tokens` (POST takes the same body plus `"operations": ["stream"]` or any of `view`/`download`/`watch`; GET lists the caller's own tokens without secrets), `DELETE /experiments/{name}/capture-tokens/{id}` (owner only; ends open streams), and `GET /webshark/streams/{token}` (outside `/api/v1`; the `pcs_` stream token is the only credential; starts streams for 5 min, ends at sign-out; revoked on logout, user, experiment or file delete, and stream tokens on experiment stop) |
| Netflow | `GET/POST/DELETE /experiments/{exp}/netflow`, `GET /experiments/{exp}/netflow/ws` |
| Subnet captures | `POST /experiments/{exp}/captureSubnet`, `POST /experiments/{exp}/stopCaptureSubnet` |
| VMs | `GET/PATCH /experiments/{exp}/vms`, `POST /experiments/{exp}/vms/actions` (`{"action": "start|pause|restart|shutdown|kill|resetDisk", "vms": [...]}`, at most 1000; a `vms.action` [background job](jobs.md), 403 when the role may act on none), `GET/PATCH/DELETE /experiments/{exp}/vms/{name}`, plus `/start`, `/stop`, `/restart`, `/redeploy`, `/shutdown`, `/reset`, `/cdrom`, `/vnc`, `/vnc/ws`, `/screenshot.png`, `/captures`, `/captures/{iface}/stream` (running capture as pcap over HTTP or websocket; `?from=now`), `/snapshots`, `/snapshots/{snapshot}`, `/commit`, `/memorySnapshot`, `/forwards`, `/forwards/{host}/{port}/ws` |
| VM mount (only with the `vm-mount` feature enabled) | `POST /experiments/{exp}/vms/{name}/mount`, `DELETE .../unmount`, `GET .../files?path=`, `GET/HEAD .../files/download?path=` (resumable), `PUT .../files/upload?path=`, `POST .../files/copy`. Mount and unmount answer 409 while the VM's mount or unmount is in progress, and unmount gives up with 409 `mount busy` after waiting 30 s for downloads |
| Disks | `GET/POST/DELETE /disks` (`GET ?refresh=true` inspects every image again), `/disks/snapshot`, `/disks/rebase`, `/disks/resize`, `/disks/commit`, `/disks/clone`, `/disks/clone/jobs` (the same clone as a cancelable [background job](jobs.md) via a hidden `.<new>.partial-<hex>` temp file, swept on startup), `/disks/rename`, `GET/HEAD /disks/download` (resumable), `GET /disks/checksum`. `GET` lists images up to 16 levels under the minimega files directory (skipping hidden names, `lost+found`, `<exp>/files`, `<exp>/tmp`, `<exp>/miniccc_responses`, and top-level `saved` and `transfer_*`; symlinked folders are not followed), plus topology images anywhere and their backing images, once each by cleaned `fullPath`, with `relativePath` (`""` outside), `outsideFilesDir`, `readOnly`, and `backingImages` as full paths; `name` is the file name `disks` RBAC checks. Outside images also need `experiments get` on an experiment using them (or must back an image the role sees), and `?expName=` needs `experiments get` (unknown: 404). Actions take query parameters (`disk`, `new` relative to the disk's folder, `backing`, `unsafe`, `size`) relative to the files directory or absolute, and answer 400 for outside or skipped paths, folders, a `new` that names no file (empty, ending in `/`, or last part `.` or `..`), and names minimega cannot take (white space, quotes, `#`, `\`, glob characters, `$`, `,`) in snapshot, commit, rebase and resize paths and in any `new`, 404 when missing, and 409 when `new` exists; clone, rename and delete act on the file directly, and clone and rename never replace a file; upload writes to the top level |
| Misc lookups | `GET /vms` (all experiments), `GET /applications`, `GET /topologies`, `GET /topologies/{topo}/scenarios`, `GET /hosts` |
| Users/Roles/Auth | `GET/POST /users`, `GET/PATCH/DELETE /users/{username}`, `POST /users/{username}/tokens`, `GET /roles`, `POST /signup`, `GET/POST /login`, `GET /logout` |
| Realtime | `GET /ws` (websocket broker for UI events/logs), `GET /logs`, `POST /console`, `GET/DELETE /console/{pid}`, `GET /console/{pid}/ws`, `POST /console/{pid}/size` (minimega console; see below). Slow `/ws` clients are closed with code 1013 and should reconnect; the live log stream may skip lines under load |
| SCORCH | `GET /experiments/{name}/scorch/pipelines`, `GET /experiments/{name}/scorch/pipelines/{run}/{loop}`, `POST/DELETE /experiments/{name}/scorch/pipelines/{run}` (start or cancel a run), `POST /experiments/{name}/scorch/pipelines/{run}/cleanup` (run only its cleanup stage) and `/clear` (forget its statuses), `GET /experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}[/ws]`, `/experiments/{name}/scorch/terminals*` |
| Jobs | `GET /jobs` (`?experiment=`, `?kind=`, `?active=true`), `GET /jobs/{id}`, `POST /jobs/{id}/cancel` (also on the unix socket), `DELETE /jobs/{id}` (dismiss a finished job), `GET/HEAD /jobs/{id}/artifact` (resumable). Owner, or `jobs` `list`/`get`/`update`/`delete` on the job's experiment (`*` for a job of none); others get 404. See [`jobs.md`](jobs.md) |
| Settings | `GET/POST /settings`, `GET /settings/password`, `GET /settings/timeout` |
| Builder | `GET/POST /builder/topologies`, `GET/PUT /builder/topologies/{name}`; the builder UI itself is served from the server root as `GET /builder` and `POST /builder/save` (outside `/api/v1`). Payloads and workflow are in [`builder.md`](builder.md) |
| Workflow | `POST /workflow/apply/{branch}` (`?dryRun=true`, `pending`, `expect`, `tag`), `POST /workflow/configs/{branch}` (`?dryRun=true`). Parameters and answers are in [`workflow.md`](workflow.md#rest-endpoints) |
| Options | `GET /options` (server-side CLI defaults like bridge-mode/deploy-mode, plus `base-dir.injects` and `workflow-dry-run`) |
| Health (server root, no token) | `GET/HEAD /healthz` (liveness: uptime, BoltDB store), `GET/HEAD /readyz` (readiness: minimega, mesh, store, qemu-img, WebShark, disk, websocket broker, runtime; `?exclude=<check>`); IETF `application/health+json`, `200` on pass or warn, `503` on fail, `output` fields only with a token or auth disabled. See `docs/content/health.md` |
| Tunneler (server root, only with the `tunneler-download` feature) | `GET /downloads/tunneler` (lists the installed builds), `GET/HEAD /downloads/tunneler/{name}` (resumable); neither needs a token |

When a config fails its schema, the config routes (`POST /configs`,
`PUT /configs/{kind}/{name}`) and the workflow routes answer 400 with the
explained lines in `metadata.validation`, one per line, and the validator's
text in `metadata.validation-raw`; see [`workflow.md`](workflow.md).

Config revisions: `GET /configs/{kind}/{name}` sends `ETag: "r<revision>"`
and `X-Phenix-Revision` (none for a config unwritten since revisions were
added; the ETag stays strong when gzipped) and answers `If-None-Match` with
`304`. `PUT` and `DELETE` take an optional `If-Match` (`"r<n>"` list or `*`):
`412` with `metadata.revision` when the config moved on, `400` when malformed.
`POST` and `PUT` return the new `ETag`. `GET /configs` returns `[]`, never
`null`.

Ordering: every `/api/v1` response has `X-Phenix-Seq` (taken as the request
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
unreadable. See `docs/content/api.md#freshness`.

`src/go/web/server.go` is the authoritative route list.

## Downloads

Single-file downloads stream from disk and resume:
`GET /experiments/{name}/files/{filename}?path=`, `GET /disks/download?disk=`,
the VM mount's `GET .../files/download?path=`, `GET /downloads/tunneler/{name}`
and a saved capture's `GET /webshark/captures/{name}`. Each also answers `HEAD`,
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
zip as they send it: `Accept-Ranges: none`, `Cache-Control: no-store`, and no
resume. Download very large files one at a time.

```bash
curl -C - -o out.pcap -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  "http://localhost:3000/api/v1/experiments/exp/files/out.pcap?path=scorch/run-0/out.pcap"
```

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
is rejected with `400` naming the key.

## VM annotations

`GET /experiments/{exp}/vms/{name}` includes the VM's own `annotations` from the
experiment's copy of the topology; VM lists and websocket VM updates carry
`null` instead. `PATCH /experiments/{exp}/vms/{name}` with
`{"annotations": {...}}` replaces the whole set (`{}` removes them all), also
while the experiment is running, where only `interface` (the VLAN it connects
to), `tags` and `annotations` may change. Values keep their JSON types, and the
same type checks as creation reject a bad value with `400` naming the key. The
response carries the saved annotations only when the role may also `get` the
VM.

```json
{"annotations": {"phenix/startup-autotunnel": ["8080:80"], "vncBanner": "lab"}}
```
