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

`src/go/web/public/docs/openapi.yml`, served rendered at `/docs/`, is the route
reference: every route `src/go/web/server.go` serves, websockets and routes
outside `/api/v1` included, with parameters, bodies, permissions and status
codes. `TestRouterMatchesOpenAPI` keeps the two in step, so read the spec
rather than guessing a path. The server also serves the raw spec at
`/docs/openapi.yml`, with `info.version` set to its build, and every `/api/v1`
response past authentication names that build in `X-Phenix-Version` (an
authentication refusal, and a `404` or `405` for an unserved path or method,
lack it). Points that are easy to miss:

- Outside `/api/v1`, at the server root: `/version`, `/features` and
  `/downloads/tunneler/{name}` (with the `tunneler-download` feature).
- Feature-gated routes: the VM mount routes need the `vm-mount` feature, and
  the `/console` routes `phenix ui --minimega-console`.
- Unmatched `/api/v1/*` requests return a JSON `404`; only non-API routes fall
  through to the SPA index. A route behind a disabled feature flag is therefore
  a real `404`, not `200 text/html`.
- `OPTIONS` on an `/api/v1` route answers `204` before auth, without running
  the route.
- VM power changes are `POST`: `.../vms/{name}/start`, `/stop`, `/restart`,
  `/shutdown`, `/reset` and `/redeploy`. Sign out with `POST /logout`.
- Paths use camelCase (`captureSubnet`, `memorySnapshot`).
- `GET /schemas/{version}` returns the whole schema file (`v0` Workflow, `v1`
  Topology/Experiment/Image/User/Role, `v2` Scenario).
  `GET /schemas/{kind}/{version}` returns one kind, only at the version it is
  stored at; any other kind or version answers `404`.
- `GET /experiments`, `/vms`, `/disks` and the experiment VM and file lists
  page with `pageNum` and `perPage` (both, from 1; `400` otherwise); without
  them they return everything. `/vms`, `/disks` and `/experiments/{name}/vms`
  also report the unpaged `total`.
- Workflow routes are covered in [`workflow.md`](workflow.md#rest-endpoints).

When a config fails its schema, the config routes (`POST /configs`,
`PUT /configs/{kind}/{name}`) and the workflow routes answer 400 with the
explained lines in `metadata.validation`, one per line, and the validator's
text in `metadata.validation-raw`; see [`workflow.md`](workflow.md).

`POST /users` and `POST /signup` answer `409` `user <name> already exists` for
a name another user has, and `500` `error creating user` for other failures.
`POST /users` answers `400` `role not found` for an unknown role before it
creates anything.
