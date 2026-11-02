# API

The phēnix web UI is built entirely on top of a REST API served by the phēnix
daemon. That same API is available for external tooling and automation, such
as CI/CD pipelines (see [Workflow](workflow.md)) or custom scripts.

## Interactive API Docs (Swagger/OpenAPI)

Browsable, interactive API documentation is generated from the project's
[OpenAPI](https://www.openapis.org/) (Swagger) specification, and is
available in two places:

* **On this documentation site**: [swagger.html](/swagger.html) (linked from
  the gear icon in the footer) is a static snapshot of the API docs, rendered
  from the `openapi.yml` spec at the time the docs were published.
* **On a running phēnix server**: every phēnix instance also hosts its own
  copy of the same interactive API docs at the `/docs/` path:

    ```text
    http://<phenix-host>:<port>/docs/
    ```

    For a default local installation, this would be:

    ```text
    http://localhost:3000/docs/
    ```

    !!! note
        `<port>` defaults to `3000` and comes from the `ui.listen-endpoint`
        setting. See [Settings](settings.md) for details on configuring this.

    Because it's built into the phēnix binary/image itself, `/docs/` on a
    running server is always in sync with the version of phēnix you're
    running, whereas [swagger.html](/swagger.html) reflects the spec as of
    the latest docs release.

    The server also serves the spec itself at `/docs/openapi.yml`, with
    `info.version` set to the server's build. Every `/api/v1` response that
    passes authentication names the same build in its `X-Phenix-Version`
    header.

The API docs are organized by tag (`Configs`, `Experiments`, `Virtual
Machines`, `Hosts`, `Applications`, `Topologies`, `Disks`, `Users`,
`Workflow`, `Jobs`, `Events`, etc.) and document every route, including request
parameters, response schemas and status codes. They also describe the
websockets, each as a `GET` that answers `101`, and the routes outside
`/api/v1`: health, features, version and the `/webshark/` routes. Only the web
UI's own files are left out.

!!! info
    The underlying OpenAPI spec is maintained at
    [`src/go/web/public/docs/openapi.yml`](https://github.com/sandialabs/sceptre-phenix/blob/main/src/go/web/public/docs/openapi.yml)
    in the [sceptre-phenix](https://github.com/sandialabs/sceptre-phenix)
    repository, and is rendered to static HTML with
    [Redoc](https://github.com/Redocly/redoc) as part of the phēnix build:
    container images, Debian packages, this site, and binaries built with
    `make build` all use the Redocly CLI version pinned in
    `scripts/api-docs.sh`. Run `make api-docs` to render it locally.

## Authentication

If UI/API authentication is enabled (see
[User Authn/Authz](user-administration.md)), API requests must include an
auth token generated from the `Users` page (in the `Admin` menu) of the web
UI, passed as the `X-Phenix-Auth-Token` header, which must carry the token as `Bearer <token>`:

```http
X-Phenix-Auth-Token: Bearer ******
```

A token in an `Authorization` header is ignored.

A browser link can pass the token in a `token` query parameter instead, without
`Bearer`. An API request without a token gets 403, and a request with a
malformed, expired or signed-out token gets 401. The API reference introduction
gives the full rules.

See [Generating User Authentication Tokens](user-administration.md#generating-user-authentication-tokens)
for details.

### Status Codes

With authentication enabled, a request without a token is refused with `403`,
and a request with a malformed, expired or signed-out token with `401`. A role
without the permission a route needs gets `403`. A few routes answer `404`
instead, so as not to reveal another user's job, console or capture token. An
unknown path under `/api/v1` answers `404`, and a method a path does not take
`405`. With authentication disabled, every request acts as `global-admin`.

Behind a reverse proxy that serves phēnix under a base path (`--base-path`),
the `Location` headers of new jobs and configs include the base path.

## Errors

Every error is JSON with a `message` saying what went wrong, a `cause` with
the underlying error text, and a `code`: a stable kind of error to match on,
since the message may change. Some errors add a `metadata` or `details`
object.

| `code` | Statuses | Meaning |
|---|---|---|
| `invalid_request` | `400`, `405`, `413`, `415`, `416`, `422` | The request cannot be acted on as sent. |
| `unauthorized` | `401` | No valid session. |
| `forbidden` | `403` | The role may not make the request. |
| `not_found` | `404`, `410` | The thing does not exist, or is hidden from the role. |
| `conflict` | `409`, `412`, `423` | At odds with the current state, such as a stale revision. |
| `rate_limited` | `429` | Too many requests. `Retry-After` says when to try again. |
| `unavailable` | `502`, `503`, `504` | The server or minimega cannot serve it now. Try again later. |
| `internal` | other `5xx` | The request failed in the server. |
| `broken_disks` | `400` | A start refused because a VM disk image or CD-ROM image is missing or its backing chain is broken. `details.disks` lists each one. See [Disk Check Before a Start](experiments.md#disk-check-before-a-start). |

```json
{"message": "job not found", "cause": "", "code": "not_found"}
```

The exceptions are the health routes, which answer in
`application/health+json`, the `/webshark/` routes that WebShark itself
calls, which answer in WebShark's own format, and a failed websocket
handshake, which is plain text.

## Response Encoding

JSON, YAML and plain-text responses are gzip-compressed for clients that send
`Accept-Encoding: gzip`, and downloads are never compressed.

## Paging

The experiment, VM, disk, job, experiment VM and experiment file lists take
`pageNum` (from 1) and `perPage` together, and return that page with a
`total` that counts the whole list. Without both, they return everything.

## Query Parameters

Boolean and enum query parameters are strict. A boolean takes `true` or
`false` (also `1`, `t`, `0` and `f`). An enum takes only the values that the
API docs list for it. Any other value, an empty value and a bare `?name` get
`400` with the code `invalid_request`, and the message names the values that
the parameter takes. An absent parameter takes its default.

## Downloads

Single-file downloads stream from disk and can be resumed: experiment files,
disk images, files in a mounted VM, tunneler builds, saved captures and the
server's log files. Each download route also answers `HEAD`, and every
response carries `Accept-Ranges: bytes`, a strong `ETag` and `Last-Modified`.

* `Range` asks for part of the file (`206`).
* `If-Range` with an earlier `ETag` honors `Range` only while the file is
  unchanged, and sends a changed file whole (`200`).
* `If-None-Match` with the `ETag` answers `304` while the file is unchanged.

`curl -C -` resumes with `Range` alone, so verify such a download with a
checksum. The `checksum` routes of experiment files and disk images return
the file's SHA-256, and answer `202` until it is computed. The zip downloads
of several files, and the CSV and JSONL downloads of a netflow capture, are
built as they are sent and cannot be resumed.

!!! note
    A reverse proxy in front of phēnix must pass `Range`, `If-Range` and the
    `ETag` through unchanged, and must not compress downloads, for resuming to
    work.

## Revisions and Conditional Requests

Every stored config has a `metadata.revision` that changes on each write (see
[Revisions](configuration.md#revisions)). `GET /configs/{kind}/{name}` returns
it as a strong `ETag`, `"r<revision>"`, and as a number in
`X-Phenix-Revision`. `POST /configs` and `PUT /configs/{kind}/{name}` return
the new revision the same way.

Send the ETag back in `If-Match` on a `PUT` or `DELETE`, and the write runs
only while the config is still at that revision. Otherwise it is refused with
`412 Precondition Failed`, whose `metadata.revision` holds the current
revision. `If-Match` is optional: a write without it replaces the config
whatever its revision. `If-None-Match` on a `GET` answers `304` while the
config is unchanged.

```bash
curl -si -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  http://localhost:3000/api/v1/configs/topology/demo | grep -i '^etag'
# ETag: "r42"

curl -s -X PUT -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  -H 'Content-Type: application/x-yaml' -H 'If-Match: "r42"' \
  --data-binary @demo.yaml http://localhost:3000/api/v1/configs/topology/demo
# 204 with ETag: "r43", or 412 if someone saved the config after you read it
```

## Freshness

Every response that passes authentication carries `X-Phenix-Seq`, a number
that only increases within one `phenix ui` process, and every message on the
`/ws` websocket carries the same sequence in its `seq` field. Data tagged
with a higher sequence is newer, so a client can order lists and live updates
by it. Responses that serve state read from minimega also carry
`X-Phenix-Observed-At`, when the data was read, and `X-Phenix-Stale: true`
when the data is older than normal, such as while an experiment starts or
stops.

## Streaming Endpoints

Three endpoints stream rather than return a document:

* `GET /experiments/{exp}/vms/{name}/captures/{interface}/stream` streams a
  running packet capture as a pcap file, over HTTP or a websocket. See
  [Streaming a Running Capture](webshark.md#streaming-a-running-capture).
* `GET /webshark/streams/{token}`, outside `/api/v1`, streams the same pcap
  with a [capture token](webshark.md#capture-tokens) as its only credential.
* `GET /console/{pid}/ws` attaches a websocket to a minimega console the
  server is running. See [minimega](minimega.md#from-the-web-ui).

### Slow clients

The server closes a `/ws` client that falls too far behind with close code
1013 (Try Again Later) and the reason `client too slow`. The web UI reconnects
and reloads what it shows, and a client of your own should do the same.

### Server shutdown

When `phenix ui` shuts down (see
[Stopping the Server](settings.md#stopping-the-server)), it closes every
websocket with close code 1001 (Going Away). Until it stops, a new websocket,
a new console and a new job answer `503 Service Unavailable` with
`Retry-After`.

## Background Jobs

Long operations run as [background jobs](jobs.md). The route that starts one
answers `202 Accepted` with the job and a `Location: /api/v1/jobs/{id}`
header, and `GET /api/v1/jobs/{id}` follows it to the end. The experiment
name `all` in `POST /api/v1/experiments/all/start` and
`POST /api/v1/experiments/all/trigger` starts such a job for every
experiment that your role may start or trigger. An
`Idempotency-Key` header makes a retry of such a request answer with the
first job instead of starting another. See
[Retrying a request that starts a job](jobs.md#retrying-a-request-that-starts-a-job).

## Health Endpoints

`GET /healthz` (liveness) and `GET /readyz` (readiness) report the server's
health in the `application/health+json` format, and answer `503` when it
fails. They are served from the server root, outside `/api/v1`, and need no
credentials. See [Health Checks](health.md).

## Integrations

The phēnix API can be used to integrate phēnix with external systems. Known
integrations include:

* [Workflow](workflow.md) - deploy a topology directory with
  `phenix workflow apply`, or drive experiment topology/scenario updates from
  a git-based CI/CD pipeline reacting to push events.
