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
  on every request in the `X-Phenix-Auth-Token` header — NOT the standard
  `Authorization` header, which is left free for proxy authentication. The token
  may also be passed as a `?token=<jwt>` query parameter, which is how browser
  links and image tags reach `/screenshot.png` and `/vnc/ws`.
- `--jwt-signing-key proxy-jwt` accepts JWTs minted by an upstream proxy, and a
  `dev|`-prefixed key enables development auth.

```bash
TOKEN=$(curl -s -u admin:password http://localhost:3000/api/v1/login | jq -r .token)
curl -H "X-Phenix-Auth-Token: $TOKEN" http://localhost:3000/api/v1/experiments
```

## Routes

| Resource | Routes |
|---|---|
| Configs | `GET/POST /configs`, `GET/PUT/DELETE /configs/{kind}/{name}`, `POST /configs/download` |
| Schemas | `GET /schemas/{version}`, `GET /schemas/{kind}/{version}` |
| Experiments | `GET/POST /experiments`, `GET /experiments/{name}`, `PATCH /experiments/{name}`, `DELETE /experiments/{name}`, `POST /experiments/{name}/start`, `POST /experiments/{name}/stop`, `GET /experiments/{name}/apps`, `POST/PUT /experiments/builder` |
| Experiment detail | `GET /experiments/{name}/topology`, `GET /experiments/{name}/topology/search`, `POST/DELETE /experiments/{name}/trigger`, `GET/POST /experiments/{name}/schedule`, `GET /experiments/{name}/soh` (state of health), `GET /experiments/{name}/captures`, `GET /experiments/{name}/files`, `GET /experiments/{name}/files/{filename}` |
| Netflow | `GET/POST/DELETE /experiments/{exp}/netflow`, `GET /experiments/{exp}/netflow/ws` |
| Subnet captures | `POST /experiments/{exp}/captureSubnet`, `POST /experiments/{exp}/stopCaptureSubnet` |
| VMs | `GET/PATCH /experiments/{exp}/vms`, `GET/PATCH/DELETE /experiments/{exp}/vms/{name}`, plus `/start`, `/stop`, `/restart`, `/redeploy`, `/shutdown`, `/reset`, `/cdrom`, `/vnc`, `/vnc/ws`, `/screenshot.png`, `/captures`, `/snapshots`, `/snapshots/{snapshot}`, `/commit`, `/memorySnapshot`, `/forwards`, `/forwards/{host}/{port}/ws` |
| VM mount (only with the `vm-mount` feature enabled) | `POST /experiments/{exp}/vms/{name}/mount`, `DELETE .../unmount`, `GET .../files?path=`, `GET .../files/download?path=`, `PUT .../files/upload?path=`, `POST .../files/copy` |
| Disks | `GET/POST/DELETE /disks`, `/disks/snapshot`, `/disks/rebase`, `/disks/resize`, `/disks/commit`, `/disks/clone`, `/disks/rename`, `/disks/download` |
| Misc lookups | `GET /vms` (all experiments), `GET /applications`, `GET /topologies`, `GET /topologies/{topo}/scenarios`, `GET /hosts` |
| Users/Roles/Auth | `GET/POST /users`, `GET/PATCH/DELETE /users/{username}`, `POST /users/{username}/tokens`, `GET /roles`, `POST /signup`, `GET/POST /login`, `GET /logout` |
| Realtime | `GET /ws` (websocket broker for UI events/logs), `GET /logs`, `POST /console`, `GET /console/{pid}/ws`, `POST /console/{pid}/size` |
| SCORCH | `GET /experiments/{name}/scorch/pipelines`, `GET /experiments/{name}/scorch/pipelines/{run}/{loop}`, `POST/DELETE /experiments/{name}/scorch/pipelines/{run}`, `GET /experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}[/ws]`, `/experiments/{name}/scorch/terminals*` |
| Settings | `GET/POST /settings`, `GET /settings/password`, `GET /settings/timeout` |
| Builder | `GET /builder/topologies[/{name}]`; the builder UI itself is served from the server root as `GET /builder` and `POST /builder/save` (outside `/api/v1`) |
| Workflow | `POST /workflow/apply/{branch}`, `POST /workflow/configs/{branch}` |
| Options | `GET /options` (server-side CLI defaults like bridge-mode/deploy-mode) |

`src/go/web/server.go` is the authoritative route list.
