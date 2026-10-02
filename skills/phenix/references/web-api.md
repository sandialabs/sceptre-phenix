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

## Routes

| Resource | Routes |
|---|---|
| Configs | `GET/POST /configs`, `GET/PUT/DELETE /configs/{kind}/{name}`, `POST /configs/download` |
| Schemas | `GET /schemas/{version}`, `GET /schemas/{kind}/{version}` |
| Experiments | `GET/POST /experiments`, `GET /experiments/{name}`, `PATCH /experiments/{name}`, `DELETE /experiments/{name}`, `POST /experiments/{name}/start`, `POST /experiments/{name}/stop`, `GET /experiments/{name}/apps`, `POST/PUT /experiments/builder` |
| Experiment detail | `GET /experiments/{name}/topology`, `GET /experiments/{name}/topology/search`, `POST/DELETE /experiments/{name}/trigger`, `GET/POST /experiments/{name}/schedule`, `GET /experiments/{name}/soh` (state of health), `GET /soh` (state of health summary for every experiment), `GET /experiments/{name}/captures`, `GET /experiments/{name}/files`, `GET/DELETE /experiments/{name}/files/{filename}?path=`, `POST /experiments/{name}/files/download` and `/files/delete` (`{"paths": [...]}`; zip download, bulk delete), `POST /experiments/{name}/webshark` (`{"path"}` or `{"vm", "interface"}`; readies a capture for WebShark, served at `/webshark/` outside `/api/v1` when installed) |
| Netflow | `GET/POST/DELETE /experiments/{exp}/netflow`, `GET /experiments/{exp}/netflow/ws` |
| Subnet captures | `POST /experiments/{exp}/captureSubnet`, `POST /experiments/{exp}/stopCaptureSubnet` |
| VMs | `GET/PATCH /experiments/{exp}/vms`, `GET/PATCH/DELETE /experiments/{exp}/vms/{name}`, plus `/start`, `/stop`, `/restart`, `/redeploy`, `/shutdown`, `/reset`, `/cdrom`, `/vnc`, `/vnc/ws`, `/screenshot.png`, `/captures`, `/captures/{iface}/stream` (running capture as pcap over HTTP or websocket; `?from=now`), `/snapshots`, `/snapshots/{snapshot}`, `/commit`, `/memorySnapshot`, `/forwards`, `/forwards/{host}/{port}/ws` |
| VM mount (only with the `vm-mount` feature enabled) | `POST /experiments/{exp}/vms/{name}/mount`, `DELETE .../unmount`, `GET .../files?path=`, `GET .../files/download?path=`, `PUT .../files/upload?path=`, `POST .../files/copy` |
| Disks | `GET/POST/DELETE /disks` (`GET ?refresh=true` inspects every image again), `/disks/snapshot`, `/disks/rebase`, `/disks/resize`, `/disks/commit`, `/disks/clone`, `/disks/rename`, `/disks/download`. `GET` lists images up to 16 levels under the minimega files directory (skipping hidden names, `lost+found`, `<exp>/files`, `<exp>/tmp`, `<exp>/miniccc_responses`, and top-level `saved` and `transfer_*`; symlinked folders are not followed), plus topology images anywhere and their backing images, once each by cleaned `fullPath`, with `relativePath` (`""` outside), `outsideFilesDir`, `readOnly`, and `backingImages` as full paths; `name` is the file name `disks` RBAC checks. Outside images also need `experiments get` on an experiment using them (or must back an image the role sees), and `?expName=` needs `experiments get` (unknown: 404). Actions take query parameters (`disk`, `new` relative to the disk's folder, `backing`, `unsafe`, `size`) relative to the files directory or absolute, and answer 400 for outside or skipped paths, folders, a `new` that names no file (empty, ending in `/`, or last part `.` or `..`), and names minimega cannot take (white space, quotes, `#`, `\`, glob characters, `$`, `,`) in snapshot, commit, rebase and resize paths and in any `new`, 404 when missing, and 409 when `new` exists; clone, rename and delete act on the file directly, and clone and rename never replace a file; upload writes to the top level |
| Misc lookups | `GET /vms` (all experiments), `GET /applications`, `GET /topologies`, `GET /topologies/{topo}/scenarios`, `GET /hosts` |
| Users/Roles/Auth | `GET/POST /users`, `GET/PATCH/DELETE /users/{username}`, `POST /users/{username}/tokens`, `GET /roles`, `POST /signup`, `GET/POST /login`, `GET /logout` |
| Realtime | `GET /ws` (websocket broker for UI events/logs), `GET /logs`, `POST /console`, `GET/DELETE /console/{pid}`, `GET /console/{pid}/ws`, `POST /console/{pid}/size` (minimega console; see below) |
| SCORCH | `GET /experiments/{name}/scorch/pipelines`, `GET /experiments/{name}/scorch/pipelines/{run}/{loop}`, `POST/DELETE /experiments/{name}/scorch/pipelines/{run}` (start or cancel a run), `POST /experiments/{name}/scorch/pipelines/{run}/cleanup` (run only its cleanup stage) and `/clear` (forget its statuses), `GET /experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}[/ws]`, `/experiments/{name}/scorch/terminals*` |
| Settings | `GET/POST /settings`, `GET /settings/password`, `GET /settings/timeout` |
| Builder | `GET/POST /builder/topologies`, `GET/PUT /builder/topologies/{name}`; the builder UI itself is served from the server root as `GET /builder` and `POST /builder/save` (outside `/api/v1`). Payloads and workflow are in [`builder.md`](builder.md) |
| Workflow | `POST /workflow/apply/{branch}`, `POST /workflow/configs/{branch}` |
| Options | `GET /options` (server-side CLI defaults like bridge-mode/deploy-mode) |
| Tunneler (server root, only with the `tunneler-download` feature) | `GET /downloads/tunneler` (lists the installed builds), `GET /downloads/tunneler/{name}`; neither needs a token |

`src/go/web/server.go` is the authoritative route list.

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
